package webfetch

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// tunnelActivity shares one idle window across both forwarding directions.
type tunnelActivity struct {
	mu          sync.Mutex
	connections [2]net.Conn
	idle        time.Duration
}

func (a *tunnelActivity) touch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	deadline := time.Now().Add(a.idle)
	for _, conn := range a.connections {
		_ = conn.SetDeadline(deadline)
	}
}

type activeTunnelReader struct {
	io.Reader
	activity *tunnelActivity
}

func (r activeTunnelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.activity.touch()
	}
	return n, err
}

func forwardProxyTunnel(ctx context.Context, upstream, downstream net.Conn, buffered io.Reader, budget *proxyBudget, idle time.Duration) {
	closeBoth := func() {
		_ = upstream.Close()
		_ = downstream.Close()
	}
	stop := context.AfterFunc(ctx, closeBoth)
	defer stop()
	activity := &tunnelActivity{connections: [2]net.Conn{upstream, downstream}, idle: idle}
	activity.touch()
	done := make(chan error, 2)
	// Both copies stop when either side closes or the request is canceled.
	go proxyTunnelCopy(done, upstream, activeTunnelReader{io.MultiReader(buffered, downstream), activity}, budget)
	go proxyTunnelCopy(done, downstream, activeTunnelReader{upstream, activity}, budget)
	<-done
	closeBoth()
	<-done
}
