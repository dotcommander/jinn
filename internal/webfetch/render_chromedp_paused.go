package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func watchRenderEvents[E any](ctx context.Context, event cdp.Event[E], tracker *renderTracker) {
	events := chromedp.Events(ctx, event)
	go func() {
		for payload, err := range events {
			if err != nil {
				tracker.fail(err)
				return
			}
			tracker.handleEvent(&payload)
		}
	}()
}

func handlePausedRequest(ctx context.Context, event *fetch.EventRequestPaused, target *url.URL, allowPrivateNetworks bool, tracker *renderTracker) {
	handlePausedRequestActions(pausedRequestActions{
		event: event, target: target, allowPrivateNetworks: allowPrivateNetworks, tracker: tracker,
		reject: func() error {
			_, err := chromedp.Call(ctx, fetch.FailRequest, fetch.FailRequestParams{RequestID: event.RequestID, ErrorReason: network.ErrorReasonBlockedByClient})
			return err
		},
		proceed: func() error {
			_, err := chromedp.Call(ctx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: event.RequestID})
			return err
		},
	})
}

// pausedRequestActions bundles one paused-request decision: the event, the
// validated target + network policy, the tracker, and the reject/proceed
// callbacks that answer the browser.
type pausedRequestActions struct {
	event                *fetch.EventRequestPaused
	target               *url.URL
	allowPrivateNetworks bool
	tracker              *renderTracker
	reject, proceed      func() error
}

func handlePausedRequestActions(actions pausedRequestActions) {
	event, target, allowPrivateNetworks, tracker, reject, proceed := actions.event, actions.target, actions.allowPrivateNetworks, actions.tracker, actions.reject, actions.proceed
	if event == nil || event.Request == nil {
		err := newCodedError(errors.New("browser paused request is missing URL details"), ErrorCodeInvalidURL, "retry the page or use a standard HTTP or HTTPS URL")
		if event != nil {
			_ = reject()
		}
		tracker.fail(err)
		return
	}

	err := validateRenderRequestURL(event.Request.URL, allowPrivateNetworks)
	if err != nil {
		if rejectErr := reject(); rejectErr != nil {
			tracker.fail(fmt.Errorf("reject browser request: %w", rejectErr))
		} else if tracker.isMainDocument(event) {
			tracker.fail(err)
		}
		return
	}
	//nolint:nestif // nested guards express one validation/state ladder
	if tracker.isMainDocument(event) && target != nil {
		if redirected, parseErr := url.Parse(event.Request.URL); parseErr == nil && !sameHost(target, redirected) {
			crossErr := newCodedError(
				fmt.Errorf("browser redirected to a different host: %s", event.Request.URL),
				ErrorCodeCrossHostRedirect,
				"fetch the redirected URL explicitly to confirm the new host",
			)
			if rejectErr := reject(); rejectErr != nil {
				tracker.fail(fmt.Errorf("reject browser request: %w", rejectErr))
			} else {
				tracker.fail(crossErr)
			}
			return
		}
	}
	if err := proceed(); err != nil {
		tracker.fail(fmt.Errorf("continue browser request: %w", err))
	}
}

func (t *renderTracker) isMainDocument(event *fetch.EventRequestPaused) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return event.ResourceType == network.ResourceTypeDocument && (t.mainFrameID == "" || string(event.FrameID) == t.mainFrameID)
}
