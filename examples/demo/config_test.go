package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// closedPort returns a port that was listening and is now closed — any
// connection attempt on it will be immediately refused.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("closedPort: listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestProbeServer_FindsListeningPort(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	// Extract the port httptest bound to.
	addr := srv.Listener.Addr().(*net.TCPAddr)
	got, err := probeServer("127.0.0.1", []int{addr.Port})
	if err != nil {
		t.Fatalf("probeServer: unexpected error: %v", err)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", addr.Port)
	if got != want {
		t.Errorf("probeServer returned %q; want http://127.0.0.1:<port>/v1/chat/completions", got)
	}
}

func TestProbeServer_SkipsNon200(t *testing.T) {
	t.Parallel()

	// Server responds to /v1/models with 404 — should not be selected.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	addr := srv.Listener.Addr().(*net.TCPAddr)
	_, err := probeServer("127.0.0.1", []int{addr.Port})
	if err == nil {
		t.Fatal("probeServer: expected error for non-200 server, got nil")
	}
}

func TestProbeServer_AllPortsRefused(t *testing.T) {
	t.Parallel()

	p := closedPort(t)
	_, err := probeServer("127.0.0.1", []int{p})
	if err == nil {
		t.Fatal("probeServer: expected error when all ports are refused, got nil")
	}
	if !strings.Contains(err.Error(), "tried ports") {
		t.Errorf("error %q missing 'tried ports'", err.Error())
	}
}

func TestProbeServer_ErrorMessageContainsPorts(t *testing.T) {
	t.Parallel()

	p1 := closedPort(t)
	p2 := closedPort(t)
	_, err := probeServer("127.0.0.1", []int{p1, p2})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tried ports") {
		t.Errorf("error %q does not contain 'tried ports'", msg)
	}
}
