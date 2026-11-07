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
	cdptarget "github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const renderNetworkIdleWindow = 500 * time.Millisecond

type chromedpRenderer struct {
	config           rendererConfig
	client           *Client
	slots            chan struct{}
	root             context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	browser          context.Context
	browserErr       error
	stopBrowser      context.CancelFunc
	stopAllocator    context.CancelFunc
	allocatorFactory func(context.Context, ...chromedp.ExecAllocatorOption) (context.Context, context.CancelFunc)
	closeOnce        sync.Once
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

	if err := renderCtx.Err(); err != nil {
		return renderedPage{}, codeContextError(err)
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

	browserCtx, err := r.browserContext(proxy.URL())
	if err != nil {
		if renderCtx.Err() != nil {
			return renderedPage{}, codeContextError(renderCtx.Err())
		}
		if isBrowserUnavailable(err) {
			return renderedPage{}, browserUnavailableError(err)
		}
		return renderedPage{}, newCodedError(fmt.Errorf("start browser: %w", err), ErrorCodeRenderFailed, "retry rendering the page")
	}
	if err := renderCtx.Err(); err != nil {
		return renderedPage{}, codeContextError(err)
	}
	tabCtx, tabCancel := chromedp.NewContext(browserCtx, chromedp.WithNewBrowserContext(func(params *cdptarget.CreateBrowserContextParams) {
		params.ProxyServer = proxy.URL()
		params.ProxyBypassList = "<-loopback>"
	}))
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

// browserContext starts the browser once on the renderer lifetime, not the page timeout.
// Chrome's default tab stays idle; each render owns a separate browser context and proxy.
func (r *chromedpRenderer) browserContext(proxyURL string) (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.root.Err(); err != nil {
		return nil, err
	}
	if r.browser != nil || r.browserErr != nil {
		return r.browser, r.browserErr
	}
	// The browser is shared across renders, so no per-render proxy flag
	// here: each render's proxy is set on its own browser context
	// (WithNewBrowserContext above). A browser-level proxy would pin
	// every later render to the first render's (now closed) proxy.
	_ = proxyURL
	allocatorOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	for name, value := range rendererChromeFlags("", r.config.UserAgent) {
		allocatorOptions = append(allocatorOptions, chromedp.Flag(name, value))
	}
	if r.config.ChromePath != "" {
		if err := validateChromePath(r.config.ChromePath); err != nil {
			r.browserErr = err
			return nil, err
		}
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(r.config.ChromePath))
	}
	factory := r.allocatorFactory
	if factory == nil {
		factory = chromedp.NewExecAllocator
	}
	allocatorCtx, allocatorCancel := factory(r.root, allocatorOptions...)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	if err := chromedp.Do(browserCtx); err != nil {
		browserCancel()
		allocatorCancel()
		r.browserErr = err
		return nil, err
	}
	r.browser = browserCtx
	r.stopBrowser = browserCancel
	r.stopAllocator = allocatorCancel
	return browserCtx, nil
}

func (r *chromedpRenderer) Close(context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cancel()
		if r.stopBrowser != nil {
			r.stopBrowser()
		}
		if r.stopAllocator != nil {
			r.stopAllocator()
		}
	})
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
		"webrtc-ip-handling-policy":       webrtcDisableNonProxiedUDP,
		"webrtc-multiple-routes-enabled":  false,
		"webrtc-nonproxied-udp-enabled":   false,
	}
	if proxyURL != "" {
		flags["proxy-server"] = proxyURL
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

var _ pageRenderer = (*chromedpRenderer)(nil)
