package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const renderNetworkIdleWindow = 500 * time.Millisecond

type chromedpRenderer struct {
	config    rendererConfig
	client    *Client
	slots     chan struct{}
	root      context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

//nolint:gosec // The cancel function is retained by chromedpRenderer and called exactly once by Close.
func newChromedpRenderer(cfg rendererConfig) (pageRenderer, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultRenderTimeout
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = DefaultRenderMaxConcurrency
	}
	if cfg.MaxRequests <= 0 {
		cfg.MaxRequests = DefaultRenderMaxRequests
	}
	if cfg.MaxNetworkBytes <= 0 {
		cfg.MaxNetworkBytes = DefaultRenderMaxNetworkBytes
	}
	if cfg.MaxHTMLBytes <= 0 {
		cfg.MaxHTMLBytes = DefaultMaxBodyBytes
	}
	if strings.TrimSpace(cfg.UserAgent) == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	client := NewClient(ClientConfig{
		Timeout:              cfg.Timeout,
		MaxBodyBytes:         cfg.MaxHTMLBytes,
		AllowPrivateNetworks: cfg.AllowPrivateNetworks,
		UserAgent:            cfg.UserAgent,
	})
	root, cancel := context.WithCancel(context.Background())
	return &chromedpRenderer{
		config: cfg,
		client: client,
		slots:  make(chan struct{}, cfg.MaxConcurrency),
		root:   root,
		cancel: cancel,
	}, nil
}

//nolint:funlen,gocognit,gocyclo,revive // Browser setup, interception, and budget checks form one bounded operation.
func (r *chromedpRenderer) Render(ctx context.Context, target *url.URL, options renderOptions) (renderedPage, error) {
	if r == nil {
		return renderedPage{}, newCodedError(errors.New("browser renderer is nil"), ErrorCodeBrowserUnavailable, "initialize the browser renderer and configure Chrome or Chromium")
	}
	if err := validateURL(target, r.client.allowPrivateNetworks); err != nil {
		return renderedPage{}, err
	}
	renderCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	stop := context.AfterFunc(r.root, cancel)
	defer stop()
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-renderCtx.Done():
		return renderedPage{}, codeContextError(renderCtx.Err())
	}

	budget := newProxyBudget(r.config.MaxRequests, r.config.MaxNetworkBytes, cancel)
	proxy, err := startRenderProxy(r.client, budget)
	if err != nil {
		return renderedPage{}, newCodedError(fmt.Errorf("start browser proxy: %w", err), ErrorCodeRenderFailed, "retry rendering the page")
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = proxy.Close(closeCtx)
	}()

	allocatorOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	for name, value := range rendererChromeFlags(proxy.URL(), r.config.UserAgent) {
		allocatorOptions = append(allocatorOptions, chromedp.Flag(name, value))
	}
	if r.config.ChromePath != "" {
		if err := validateChromePath(r.config.ChromePath); err != nil {
			return renderedPage{}, browserUnavailableError(err)
		}
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(r.config.ChromePath))
	}
	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(renderCtx, allocatorOptions...)
	defer allocatorCancel()
	tabCtx, tabCancel := chromedp.NewContext(allocatorCtx)
	defer tabCancel()

	tracker := newRenderTracker(r.config.MaxRequests, cancel)
	watchRenderEvents(tabCtx, network.RequestWillBeSent, tracker)
	watchRenderEvents(tabCtx, network.ResponseReceived, tracker)
	watchRenderEvents(tabCtx, network.LoadingFinished, tracker)
	watchRenderEvents(tabCtx, network.LoadingFailed, tracker)
	pausedEvents := chromedp.Events(tabCtx, fetch.RequestPaused)
	go func() {
		for event, err := range pausedEvents {
			if err != nil {
				tracker.fail(err)
				return
			}
			go handlePausedRequest(tabCtx, &event, target, r.client.allowPrivateNetworks, tracker)
		}
	}()

	var page renderedPage
	var snapshot renderSnapshot
	actions := []chromedp.Action[chromedp.Void]{
		renderCommand(network.Enable, network.EnableParams{}),
		renderCommand(fetch.Enable, fetch.EnableParams{}),
		renderCommand(browser.SetDownloadBehavior, browser.SetDownloadBehaviorParams{Behavior: browser.SetDownloadBehaviorBehaviorDeny}),
		chromedp.Navigate(target.String()),
	}
	if options.Wait == RenderWaitNetworkIdle {
		actions = append(actions, chromedp.Func(func(context.Context, *chromedp.Target) error {
			return tracker.waitForIdle(renderCtx, renderNetworkIdleWindow)
		}))
	}
	actions = append(actions, chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		var err error
		page.FinalURL, err = chromedp.Run(ctx, chromedp.Location())
		if err != nil {
			return err
		}
		snapshot, err = chromedp.Run(ctx, chromedp.Evaluate[renderSnapshot](renderSnapshotExpression(r.config.MaxHTMLBytes)))
		return err
	}))
	if err := chromedp.Do(tabCtx, actions...); err != nil {
		if budgetErr := firstRenderBudgetError(budget.Err(), tracker.Err()); budgetErr != nil {
			return renderedPage{}, budgetErr
		}
		if renderCtx.Err() != nil {
			return renderedPage{}, codeContextError(renderCtx.Err())
		}
		if isBrowserUnavailable(err) {
			return renderedPage{}, browserUnavailableError(err)
		}
		return renderedPage{}, newCodedError(fmt.Errorf("render page: %w", err), ErrorCodeRenderFailed, "retry the page or use static Defuddle extraction")
	}
	if redirectErr := checkRenderedFinalURL(target, page.FinalURL); redirectErr != nil {
		return renderedPage{}, redirectErr
	}
	if budgetErr := firstRenderBudgetError(budget.Err(), tracker.Err()); budgetErr != nil {
		return renderedPage{}, budgetErr
	}
	if snapshot.Bytes > r.config.MaxHTMLBytes || int64(len(snapshot.HTML)) > r.config.MaxHTMLBytes {
		return renderedPage{}, newCodedError(
			fmt.Errorf("rendered HTML budget exceeded: %d bytes exceeds %d", snapshot.Bytes, r.config.MaxHTMLBytes),
			ErrorCodeRenderBudget,
			"reduce the page size or increase the server render HTML budget",
		)
	}
	page.StatusCode, page.ContentType = tracker.mainResponse()
	page.HTML = snapshot.HTML
	return page, nil
}

func renderCommand[P, R any](command cdp.Command[P, R], params P) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		_, err := chromedp.Call(ctx, command, params)
		return err
	})
}

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

func checkRenderedFinalURL(target *url.URL, finalURL string) error {
	if target == nil || strings.TrimSpace(finalURL) == "" {
		return nil
	}
	final, err := url.Parse(finalURL)
	// Malformed final URLs are ignored, matching the redirect handler.
	if err == nil && sameHost(target, final) {
		return nil
	}
	return newCodedError(
		fmt.Errorf("rendered page redirected to a different host: %s", finalURL),
		ErrorCodeCrossHostRedirect,
		"fetch the redirected URL explicitly to confirm the new host",
	)
}

func validateRenderRequestURL(rawURL string, allowPrivateNetworks bool) error {
	target, err := url.Parse(rawURL)
	if err != nil {
		return newCodedError(fmt.Errorf("parse browser request URL: %w", err), ErrorCodeInvalidURL, "retry the page or use a standard HTTP or HTTPS URL")
	}
	return validateURL(target, allowPrivateNetworks)
}

func (r *chromedpRenderer) Close(context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(r.cancel)
	return nil
}

func rendererChromeFlags(proxyURL, userAgent string) map[string]any {
	flags := map[string]any{
		"disable-popup-blocking":          false,
		"disable-quic":                    true,
		"force-webrtc-ip-handling-policy": webrtcDisableNonProxiedUDP,
		"host-resolver-rules":             "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1",
		"no-sandbox":                      false,
		"proxy-bypass-list":               "<-loopback>",
		"proxy-server":                    proxyURL,
		"webrtc-ip-handling-policy":       webrtcDisableNonProxiedUDP,
		"webrtc-multiple-routes-enabled":  false,
		"webrtc-nonproxied-udp-enabled":   false,
	}
	if strings.TrimSpace(userAgent) != "" {
		flags["user-agent"] = userAgent
	}
	return flags
}

func validateChromePath(path string) error {
	resolved, err := exec.LookPath(path)
	if err != nil {
		return fmt.Errorf("find Chrome executable %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat Chrome executable %q: %w", resolved, err)
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		return fmt.Errorf("chrome path %q is not executable", resolved)
	}
	return nil
}

func browserUnavailableError(err error) error {
	return newCodedError(fmt.Errorf("start Chrome or Chromium: %w", err), ErrorCodeBrowserUnavailable, "install Chrome or Chromium and configure its executable path")
}

func isBrowserUnavailable(err error) bool {
	var execErr *exec.Error
	var pathErr *os.PathError
	return errors.As(err, &execErr) || errors.As(err, &pathErr) ||
		strings.Contains(strings.ToLower(err.Error()), "executable file not found") ||
		strings.Contains(strings.ToLower(err.Error()), "cannot find chrome")
}

func firstRenderBudgetError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

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

var _ pageRenderer = (*chromedpRenderer)(nil)
