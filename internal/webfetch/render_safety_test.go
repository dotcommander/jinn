package webfetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return target
}

func TestRenderRejectsCrossHostMainDocument(t *testing.T) {
	t.Parallel()
	target := mustParseURL(t, "http://origin.example/")

	// Main-document redirect to another host: rejected, tracker fails.
	ctx, cancel := context.WithCancel(t.Context())
	tracker := newRenderTracker(10, cancel)
	tracker.mainFrameID = "main"
	rejected := false
	handlePausedRequestActions(
		&fetch.EventRequestPaused{ResourceType: network.ResourceTypeDocument, FrameID: "main", Request: &network.Request{URL: "http://other.example/page"}},
		target, true, tracker,
		func() error { rejected = true; return nil }, func() error { t.Fatal("cross-host document continued"); return nil })
	if !rejected {
		t.Fatal("cross-host main document was not rejected")
	}
	assertCodedError(t, tracker.Err(), ErrorCodeCrossHostRedirect)
	if ctx.Err() == nil {
		t.Fatal("cross-host main document did not cancel rendering")
	}
	cancel()

	// Cross-host subresource: allowed (donor parity: sub-resource redirects are fine).
	tracker = newRenderTracker(10, nil)
	tracker.mainFrameID = "main"
	continued := false
	handlePausedRequestActions(
		&fetch.EventRequestPaused{ResourceType: network.ResourceTypeImage, FrameID: "main", Request: &network.Request{URL: "http://cdn.example/image.png"}},
		target, true, tracker,
		func() error { t.Fatal("cross-host subresource rejected"); return nil }, func() error { continued = true; return nil })
	if !continued || tracker.Err() != nil {
		t.Fatalf("cross-host subresource: continued=%v error=%v", continued, tracker.Err())
	}
}

func TestCheckRenderedFinalURL(t *testing.T) {
	t.Parallel()
	target := mustParseURL(t, "http://origin.example/page")
	if err := checkRenderedFinalURL(target, "http://origin.example/final"); err != nil {
		t.Fatalf("same-host final: %v", err)
	}
	if err := checkRenderedFinalURL(target, "https://origin.example/final"); err != nil {
		t.Fatalf("same-host upgrade final: %v", err)
	}
	assertCodedError(t, checkRenderedFinalURL(target, "http://other.example/final"), ErrorCodeCrossHostRedirect)
	assertCodedError(t, checkRenderedFinalURL(target, "http://api.origin.example/final"), ErrorCodeCrossHostRedirect)
	if err := checkRenderedFinalURL(target, ""); err != nil {
		t.Fatalf("empty final: %v", err)
	}
}

func TestRenderRejectsSubresourcesWithoutCancelingDocument(t *testing.T) {
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
			targetURL := "http://127.0.0.1/"
			handlePausedRequestActions(&fetch.EventRequestPaused{ResourceType: resource, FrameID: frame, Request: &network.Request{URL: rawURL}}, mustParseURL(t, targetURL), false, tracker,
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
	handlePausedRequestActions(&fetch.EventRequestPaused{ResourceType: network.ResourceTypeImage, Request: &network.Request{URL: "http://127.0.0.1/"}}, mustParseURL(t, "http://127.0.0.1/"), false, tracker,
		func() error { return errors.New("CDP unavailable") }, func() error { return nil })
	if tracker.Err() == nil {
		t.Fatal("infrastructure failure was ignored")
	}
}

func TestAttributeTokens(t *testing.T) {
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

func TestRenderTunnelRefreshesIdleDeadlineAndCancels(t *testing.T) {
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

func TestRenderIdleTunnelCloses(t *testing.T) {
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
