// Package session reads established inbound TCP sessions from ss.
// A failed check is reported as unknown. It is never treated as proof that
// every device disconnected.
package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Endpoint is the remote address of an established socket and the local port
// it connected to. Syslog identity is the source IP plus the listener port.
type Endpoint struct {
	IP   string
	Port int
}

// Lister returns established TCP sessions.
type Lister interface {
	Established(ctx context.Context) (map[Endpoint]struct{}, error)
}

// Command runs ss.
type Command struct{}

// Established returns sessions keyed by remote IP and local port.
func (Command) Established(ctx context.Context) (map[Endpoint]struct{}, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ss", "-H", "-t", "-n", "state", "established")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("TCP check unavailable: %s", err)
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return nil, fmt.Errorf("TCP check unavailable: %s", err)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("TCP check failed: %s", msg)
	}
	sessions, err := ParseEstablished(stdout.String())
	if err != nil {
		return nil, errors.New("TCP check returned unrecognized ss output")
	}
	return sessions, nil
}

// ParseEstablished extracts endpoints from `ss -H -t -n state established`.
// Endpoints are the last two columns so a leading state token is tolerated.
func ParseEstablished(output string) (map[Endpoint]struct{}, error) {
	sessions := map[Endpoint]struct{}{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 4 {
			return nil, errors.New("unrecognized ss output")
		}
		_, localPort, err := splitEndpoint(fields[len(fields)-2])
		if err != nil {
			return nil, err
		}
		remoteIP, _, err := splitEndpoint(fields[len(fields)-1])
		if err != nil {
			return nil, err
		}
		sessions[Endpoint{IP: remoteIP, Port: localPort}] = struct{}{}
	}
	return sessions, nil
}

func splitEndpoint(token string) (string, int, error) {
	var host, portStr string
	if strings.HasPrefix(token, "[") {
		cut := strings.LastIndex(token, "]:")
		if cut < 0 {
			return "", 0, errors.New("unrecognized ss endpoint")
		}
		host = token[1:cut]
		portStr = token[cut+2:]
	} else {
		cut := strings.LastIndex(token, ":")
		if cut < 0 {
			return "", 0, errors.New("unrecognized ss endpoint")
		}
		host = token[:cut]
		portStr = token[cut+1:]
	}
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return "", 0, errors.New("unrecognized ss endpoint")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", 0, errors.New("unrecognized ss endpoint")
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String(), port, nil
	}
	return ip.String(), port, nil
}
