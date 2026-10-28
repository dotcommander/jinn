package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func serveInspectorServer(ctx context.Context, srv *http.Server, listener net.Listener, grace time.Duration) error {
	return serveInspectorServerWithDrain(ctx, srv, listener, grace, nil)
}

// onDrained takes ownership of handler resources. A forced shutdown returns
// after cancellation and connection closure; the coordinator retains those
// resources until every admitted handler exits, then invokes onDrained once.
func serveInspectorServerWithDrain(ctx context.Context, srv *http.Server, listener net.Listener, grace time.Duration, onDrained func()) error {
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	srv.BaseContext = func(net.Listener) context.Context { return requestCtx }
	var mu sync.Mutex
	var active sync.WaitGroup
	closing := false
	handler := srv.Handler
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if closing {
			mu.Unlock()
			http.Error(w, "inspector is shutting down", http.StatusServiceUnavailable)
			return
		}
		active.Add(1)
		mu.Unlock()
		defer active.Done()
		handler.ServeHTTP(w, r)
	})
	serveCtx, stopServing := context.WithCancel(sigCtx)
	defer stopServing()
	shutdownDone := make(chan error, 1)
	go func() {
		// Cancellation or Serve failure ends this shutdown coordinator.
		<-serveCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			cancelRequests()
			_ = srv.Close()
		}
		mu.Lock()
		closing = true
		mu.Unlock()
		if err != nil {
			shutdownDone <- err
		}
		active.Wait()
		if onDrained != nil {
			onDrained()
		}
		if err == nil {
			shutdownDone <- nil
		}
	}()
	err := srv.Serve(listener)
	stopServing()
	shutdownErr := <-shutdownDone
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
