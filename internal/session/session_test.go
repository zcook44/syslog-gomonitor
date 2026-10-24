package session

import (
	"context"
	"net"
	"os/exec"
	"testing"
)

func TestParseEstablished(t *testing.T) {
	output := "" +
		"0 0 192.0.2.50:514 192.0.2.10:58433\n" +
		"ESTAB 0 0 [2001:db8::50]:601 [2001:db8::10]:12345\n" +
		"0 0 [::ffff:192.0.2.50]:514 [::ffff:192.0.2.11]:12345\n"
	got, err := ParseEstablished(output)
	if err != nil {
		t.Fatal(err)
	}
	want := []Endpoint{
		{IP: "192.0.2.10", Port: 514},
		{IP: "2001:db8::10", Port: 601},
		{IP: "192.0.2.11", Port: 514},
	}
	if len(got) != len(want) {
		t.Fatalf("sessions %#v", got)
	}
	for _, endpoint := range want {
		if _, ok := got[endpoint]; !ok {
			t.Fatalf("missing %v in %#v", endpoint, got)
		}
	}
	if _, err := ParseEstablished("not-an-ss-row"); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestCommandSeesLoopbackSession(t *testing.T) {
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}
	client, err := dialer.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	sessions, err := Command{}.Established(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions[Endpoint{IP: "127.0.0.2", Port: port}]; !ok {
		t.Fatalf("session 127.0.0.2:%d not in ss output (%d sessions)", port, len(sessions))
	}
}
