package webfetch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
)

type renderSnapshot struct {
	Bytes int64  `json:"bytes"`
	HTML  string `json:"html"`
}

func renderSnapshotExpression(maxBytes int64) string {
	return fmt.Sprintf(`(() => {
		const html = document.documentElement ? document.documentElement.outerHTML : "";
		const bytes = new TextEncoder().encode(html).length;
		return bytes > %d ? {bytes} : {bytes, html};
	})()`, maxBytes)
}

type renderTracker struct {
	mu           sync.Mutex
	maxRequests  int
	requests     int
	redirects    int
	inFlight     map[network.RequestID]network.ResourceType
	lastActivity time.Time
	mainFrameID  string
	statusCode   int
	contentType  string
	err          error
	cancel       context.CancelFunc
}

func newRenderTracker(maxRequests int, cancel context.CancelFunc) *renderTracker {
	return &renderTracker{
		maxRequests:  maxRequests,
		inFlight:     make(map[network.RequestID]network.ResourceType),
		lastActivity: time.Now(),
		cancel:       cancel,
	}
}

//nolint:gocyclo // The event switch is the direct state machine for browser budget tracking.
func (t *renderTracker) handleEvent(event any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return
	}
	switch event := event.(type) {
	case *network.EventRequestWillBeSent:
		t.requests++
		if t.maxRequests > 0 && t.requests > t.maxRequests {
			t.failLocked(fmt.Errorf("browser request budget exceeded: %d requests exceeds %d", t.requests, t.maxRequests))
			return
		}
		if event.Type == network.ResourceTypeDocument && t.mainFrameID == "" {
			t.mainFrameID = string(event.FrameID)
		}
		if event.Type == network.ResourceTypeDocument && string(event.FrameID) == t.mainFrameID && event.RedirectResponse != nil {
			t.redirects++
			if t.redirects > maxRedirectHops {
				t.failLocked(fmt.Errorf("browser redirect budget exceeded: %d redirects exceeds %d", t.redirects, maxRedirectHops))
				return
			}
		}
		if !ignoreForNetworkIdle(event.Type) {
			t.inFlight[event.RequestID] = event.Type
		}
		t.lastActivity = time.Now()
	case *network.EventLoadingFinished:
		delete(t.inFlight, event.RequestID)
		t.lastActivity = time.Now()
	case *network.EventLoadingFailed:
		delete(t.inFlight, event.RequestID)
		t.lastActivity = time.Now()
	case *network.EventResponseReceived:
		if event.Type == network.ResourceTypeDocument && string(event.FrameID) == t.mainFrameID && event.Response != nil {
			t.statusCode = int(event.Response.Status)
			t.contentType = responseContentType(event.Response)
		}
	}
}

func (t *renderTracker) failLocked(err error) {
	t.err = newCodedError(err, ErrorCodeRenderBudget, "reduce page activity or increase the server render budget")
	if t.cancel != nil {
		t.cancel()
	}
}

func (t *renderTracker) fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return
	}
	var coded *CodedError
	if errors.As(err, &coded) {
		t.err = err
	} else {
		t.err = newCodedError(err, ErrorCodeRenderFailed, "retry the page or use static Defuddle extraction")
	}
	if t.cancel != nil {
		t.cancel()
	}
}

func (t *renderTracker) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *renderTracker) mainResponse() (int, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.statusCode, t.contentType
}

func (t *renderTracker) waitForIdle(ctx context.Context, quietWindow time.Duration) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		t.mu.Lock()
		idle := len(t.inFlight) == 0 && time.Since(t.lastActivity) >= quietWindow
		err := t.err
		t.mu.Unlock()
		if err != nil {
			return err
		}
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func ignoreForNetworkIdle(resourceType network.ResourceType) bool {
	return resourceType == network.ResourceTypeEventSource || resourceType == network.ResourceTypeWebSocket
}

func responseContentType(response *network.Response) string {
	if response == nil {
		return ""
	}
	for name, value := range response.Headers {
		if strings.EqualFold(name, "Content-Type") {
			if contentType, ok := value.(string); ok {
				return contentType
			}
		}
	}
	return response.MimeType
}
