package webfetch

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
)

func TestAuditC501RejectsSubresourcesWithoutCancelingDocument(t *testing.T) {
	t.Parallel()
	for _, resource := range []network.ResourceType{network.ResourceTypeImage, network.ResourceTypeScript, network.ResourceTypeDocument} {
		for _, rawURL := range []string{"http://127.0.0.1/private", "data:image/png;base64,eA=="} {
			ctx, cancel := context.WithCancel(t.Context())
			tracker := newRenderTracker(10, cancel)
			tracker.mainFrameID = "main"
			frame := cdp.FrameID("child")
			if resource == network.ResourceTypeDocument {
				frame = "main"
			}
			rejected := false
			handlePausedRequestActions(&fetch.EventRequestPaused{ResourceType: resource, FrameID: frame, Request: &network.Request{URL: rawURL}}, false, tracker,
				func() error { rejected = true; return nil }, func() error { t.Fatal("unsafe request continued"); return nil })
			if !rejected || (tracker.Err() != nil) != (resource == network.ResourceTypeDocument) {
				t.Fatalf("resource %s: rejected=%v error=%v", resource, rejected, tracker.Err())
			}
			if (ctx.Err() != nil) != (resource == network.ResourceTypeDocument) {
				t.Fatalf("resource %s canceled=%v", resource, ctx.Err())
			}
			cancel()
		}
	}
	tracker := newRenderTracker(10, nil)
	handlePausedRequestActions(&fetch.EventRequestPaused{ResourceType: network.ResourceTypeImage, Request: &network.Request{URL: "http://127.0.0.1/"}}, false, tracker,
		func() error { return errors.New("CDP unavailable") }, func() error { return nil })
	if tracker.Err() == nil {
		t.Fatal("infrastructure failure was ignored")
	}
}

func TestAuditC502AttributeTokens(t *testing.T) {
	t.Parallel()
	for _, marker := range []string{"apple", "appointment", "root-cause", "myapp", "reactive"} {
		if got := classifyHTML(`<div id="` + marker + `">Static article</div>`); got != shellClassStatic {
			t.Fatalf("%s classified %v", marker, got)
		}
	}
	for _, marker := range []string{"app", "root", "__next", "__nuxt", "react"} {
		if got := classifyHTML(`<div class="container ` + marker + `"></div><script>boot()</script>`); got != shellClassLikely {
			t.Fatalf("%s classified %v", marker, got)
		}
	}
	if got := classifyHTML(`<div data-id="app">An ordinary article</div>`); got != shellClassStatic {
		t.Fatalf("unrelated attribute classified %v", got)
	}
}

func TestAuditC504TunnelRefreshesIdleDeadlineAndCancels(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		upstream, origin := net.Pipe()
		downstream, browser := net.Pipe()
		t.Cleanup(func() { _ = origin.Close(); _ = browser.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			forwardProxyTunnel(ctx, upstream, downstream, strings.NewReader(""), newProxyBudget(0, 100, nil), 10*time.Second)
			close(done)
		}()
		for range 3 {
			time.Sleep(6 * time.Second)
			writeDone := make(chan error, 1)
			go func() { _, err := io.WriteString(origin, "x"); writeDone <- err }()
			var b [1]byte
			if _, err := io.ReadFull(browser, b[:]); err != nil {
				t.Fatalf("active transfer stopped: %v", err)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("canceled tunnel did not terminate")
		}
	})
}

func TestAuditC504IdleTunnelCloses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		upstream, origin := net.Pipe()
		downstream, browser := net.Pipe()
		t.Cleanup(func() { _ = origin.Close(); _ = browser.Close() })
		done := make(chan struct{})
		go func() {
			forwardProxyTunnel(t.Context(), upstream, downstream, strings.NewReader(""), newProxyBudget(0, 100, nil), time.Second)
			close(done)
		}()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("idle tunnel did not terminate")
		}
	})
}
