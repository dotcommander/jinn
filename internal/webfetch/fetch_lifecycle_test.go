package webfetch

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceClosePreventsLazyRendererInitialization(t *testing.T) {
	t.Parallel()
	service := NewService(Config{})
	var factoryCalls atomic.Int32
	factoryStarted := make(chan struct{})
	factoryProceed := make(chan struct{})
	renderer := &fakePageRenderer{}
	service.renderFactory = func(rendererConfig) (pageRenderer, error) {
		factoryCalls.Add(1)
		close(factoryStarted)
		<-factoryProceed
		return renderer, nil
	}
	initializeDone := make(chan error, 1)
	go func() {
		_, err := service.ensureRenderer(context.Background())
		initializeDone <- err
	}()
	<-factoryStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(context.Background()) }()
	for {
		service.renderMu.Lock()
		closed := service.closed
		service.renderMu.Unlock()
		if closed {
			break
		}
		runtime.Gosched()
	}
	close(factoryProceed)
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := <-initializeDone
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeInvalidArgument {
		t.Fatalf("ensureRenderer error = %#v", err)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("renderer factory calls = %d", got)
	}
	if got := renderer.closed.Load(); got != 1 {
		t.Fatalf("renderer close calls = %d", got)
	}
}

func TestServiceCloseContextBoundsRendererInitialization(t *testing.T) {
	t.Parallel()
	service := NewService(Config{})
	factoryStarted := make(chan struct{})
	factoryProceed := make(chan struct{})
	service.renderFactory = func(rendererConfig) (pageRenderer, error) {
		close(factoryStarted)
		<-factoryProceed
		return &fakePageRenderer{}, nil
	}
	initializeDone := make(chan error, 1)
	go func() {
		_, err := service.ensureRenderer(context.Background())
		initializeDone <- err
	}()
	<-factoryStarted
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := service.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v", err)
	}
	close(factoryProceed)
	if err := <-initializeDone; err == nil {
		t.Fatal("initialization succeeded after bounded Close")
	}
}

func TestServiceCloseAfterLazyInitializationClosesRendererOnce(t *testing.T) {
	t.Parallel()
	service := NewService(Config{})
	renderer := &fakePageRenderer{}
	service.renderFactory = func(rendererConfig) (pageRenderer, error) { return renderer, nil }
	if _, err := service.ensureRenderer(context.Background()); err != nil {
		t.Fatalf("ensureRenderer: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := renderer.closed.Load(); got != 1 {
		t.Fatalf("renderer close calls = %d", got)
	}
}
