// Package server publishes the status page and a live event stream.
// The listener is local unless the operator chooses another address.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"syslog-gomonitor/internal/dashboard"
	"syslog-gomonitor/internal/engine"
	"syslog-gomonitor/internal/inventory"
)

// Listen serves the dashboard until ctx is cancelled.
func Listen(ctx context.Context, addr string, eng *engine.Engine) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           Handler(eng),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Handler routes the dashboard, JSON status, and event stream.
func Handler(eng *engine.Engine) http.Handler {
	mux := http.NewServeMux()
	h := &handler{eng: eng}
	mux.HandleFunc("/healthz", h.health)
	mux.HandleFunc("/api/status", h.api)
	mux.HandleFunc("/events", h.events)
	mux.HandleFunc("/", h.page)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		switch r.URL.Path {
		case "/api/adopt", "/api/ignore", "/api/unignore":
			h.mutation(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type handler struct {
	eng *engine.Engine
}

func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	body, err := dashboard.Page(h.eng.Latest())
	if err != nil {
		http.Error(w, "status page failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func (h *handler) api(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(true)
	if err := enc.Encode(h.eng.Latest()); err != nil {
		http.Error(w, "status encoding failed", http.StatusInternalServerError)
	}
}

func (h *handler) mutation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeAPI(w, http.StatusMethodNotAllowed, apiError("Method not allowed."))
		return
	}
	if !sameOrigin(r) {
		writeAPI(w, http.StatusForbidden, apiError("Cross-site request refused."))
		return
	}
	switch r.URL.Path {
	case "/api/adopt":
		var body struct {
			Name     string `json:"name"`
			SourceIP string `json:"source_ip"`
			Protocol string `json:"protocol"`
			TCPPort  *int   `json:"tcp_port"`
			LogPath  string `json:"log_path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		in := inventory.Adoption{
			Name:     body.Name,
			SourceIP: body.SourceIP,
			Protocol: body.Protocol,
			LogPath:  body.LogPath,
		}
		if body.TCPPort != nil {
			in.TCPPort = *body.TCPPort
		}
		h.finish(w, r, h.eng.Adopt(r.Context(), in))
	case "/api/ignore":
		var body struct {
			LogPath string `json:"log_path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		h.finish(w, r, h.eng.Ignore(r.Context(), body.LogPath))
	case "/api/unignore":
		var body struct {
			LogPath string `json:"log_path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		h.finish(w, r, h.eng.Unignore(r.Context(), body.LogPath))
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) finish(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		writeAPI(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	var user *inventory.UserError
	if errors.As(err, &user) {
		writeAPI(w, http.StatusBadRequest, apiError(user.Message))
		return
	}
	writeAPI(w, http.StatusInternalServerError, apiError("The inventory could not be updated."))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeAPI(w, http.StatusUnsupportedMediaType, apiError("Send a JSON request."))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeAPI(w, http.StatusBadRequest, apiError("The request could not be read."))
		return false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		writeAPI(w, http.StatusBadRequest, apiError("The request must be a single JSON object."))
		return false
	}
	return true
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-Requested-With") != "syslog-gomonitor" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func apiError(message string) map[string]string {
	return map[string]string{"error": message}
}

func writeAPI(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(body)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() error {
		payload, err := dashboard.Event(h.eng.Latest())
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	ch := h.eng.Subscribe()
	defer h.eng.Unsubscribe(ch)
	if err := send(); err != nil {
		return
	}
	keepalive := time.NewTicker(10 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if err := send(); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'none'; base-uri 'none'; form-action 'none'")
}
