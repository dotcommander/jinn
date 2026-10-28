package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
)

func TestAuditC105(t *testing.T) {
	t.Parallel()
	for _, forced := range []bool{false, true} {
		name := "graceful"
		if forced {
			name = "forced"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				defer close(finished)
				if forced {
					<-r.Context().Done()
					return
				}
				<-release
				_, _ = io.WriteString(w, "complete")
			})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			grace := 5 * time.Second
			if forced {
				grace = 0
			}
			done := make(chan error, 1)
			go func() {
				done <- serveInspectorServer(ctx, &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}, listener, grace)
			}()
			responseDone := make(chan string, 1)
			go func() {
				// Request completion ends this owned client goroutine.
				response, err := http.Get("http://" + listener.Addr().String())
				if err != nil {
					responseDone <- err.Error()
					return
				}
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				responseDone <- string(body)
			}()
			<-entered
			cancel()
			if !forced {
				select {
				case err := <-done:
					t.Fatalf("returned before handler completion: %v", err)
				default:
				}
				close(release)
			}
			err = <-done
			if forced && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("forced shutdown error=%v", err)
			}
			if !forced && err != nil {
				t.Fatal(err)
			}
			<-finished
			if body := <-responseDone; !forced && body != "complete" {
				t.Fatalf("graceful body=%q", body)
			}
		})
	}
}

func TestAuditC105UncooperativeHandlerRetainsEngine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("still available"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := jinn.New(root, "audit")
	entered, release := make(chan struct{}), make(chan struct{})
	handlerResult, engineClosed := make(chan error, 1), make(chan error, 1)
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		// Simulate synchronous filesystem work that cannot observe cancellation.
		<-release
		_, _, err := engine.Dispatch(r.Context(), "read_file", map[string]any{"path": "probe.txt"})
		handlerResult <- err
		w.WriteHeader(http.StatusNoContent)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- serveInspectorServerWithDrain(ctx, srv, listener, 0, func() { engineClosed <- engine.Close() })
	}()
	requestDone := make(chan struct{})
	go func() {
		// Closed connection or normal response ends this client goroutine.
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
		close(requestDone)
	}()
	<-entered
	cancel()
	deadline, stop := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(stop)
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("forced shutdown error=%v", err)
		}
	case <-deadline.Done():
		close(release)
		t.Fatal("shutdown waited for an uncooperative handler")
	}
	select {
	case err := <-engineClosed:
		close(release)
		t.Fatalf("engine closed before handler release: %v", err)
	default:
	}
	close(release)
	if err := <-handlerResult; err != nil {
		t.Fatalf("handler accessed prematurely closed engine: %v", err)
	}
	if err := <-engineClosed; err != nil {
		t.Fatalf("close drained engine: %v", err)
	}
	<-requestDone
}

func TestAuditC1R01(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		host string
		want int
	}{{"[::1]", 204}, {"[::1]:8787", 204}, {"::1", 204}, {"127.0.0.1", 204}, {"127.0.0.1:8787", 204}, {"localhost", 204}, {"192.0.2.1", 403}, {"[2001:db8::1]", 403}, {"[2001:db8::1]:8787", 403}, {"foreign.example", 403}, {"[::1", 403}, {"::1]", 403}, {"[[::1]]", 403}, {"[::1]extra", 403}} {
		t.Run(test.host, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest("GET", "http://localhost/", nil)
			request.Host = test.host
			response := httptest.NewRecorder()
			inspectorSecurityHeaders("audit", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("host=%q status=%d want=%d", test.host, response.Code, test.want)
			}
		})
	}
	t.Log("bracketed loopback works with and without a port; foreign and malformed hosts remain rejected")
}
