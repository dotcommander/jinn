package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/dotcommander/jinn/internal/webfetch"
)

const webCloseTimeout = 5 * time.Second

// webConfig is the sole command-layer environment reader for the web service.
func webConfig() webfetch.Config {
	cacheDir := strings.TrimSpace(os.Getenv("JINN_WEB_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = defaultJinnWebCacheDir()
	}
	return webfetch.Config{
		UserAgent: "jinn/" + jinn.ResolveVersion(version), JinaAPIKey: strings.TrimSpace(os.Getenv("JINA_API_KEY")), BraveAPIKey: strings.TrimSpace(os.Getenv("BRAVE_API_KEY")), ExaAPIKey: strings.TrimSpace(os.Getenv("EXA_API_KEY")),
		ReaderMode: strings.TrimSpace(os.Getenv("JINN_WEB_READER")), ReaderEndpoint: strings.TrimSpace(os.Getenv("JINN_WEB_READER_ENDPOINT")), SearchEndpoint: strings.TrimSpace(os.Getenv("JINN_WEB_SEARCH_ENDPOINT")), ExaSearchEndpoint: strings.TrimSpace(os.Getenv("JINN_WEB_EXA_SEARCH_ENDPOINT")), SearchProvider: strings.TrimSpace(os.Getenv("JINN_WEB_SEARCH_PROVIDER")),
		URLCacheTTL: webDurationEnv("JINN_WEB_CACHE_TTL"), URLCacheDir: cacheDir, Timeout: webDurationEnv("JINN_WEB_TIMEOUT"), MaxBodyBytes: webInt64Env("JINN_WEB_MAX_BODY_BYTES"), AllowPrivateNetworks: webBoolEnv("JINN_WEB_ALLOW_PRIVATE_NETWORKS"),
		RenderTimeout: webDurationEnv("JINN_WEB_RENDER_TIMEOUT"), RenderMaxConcurrency: webIntEnv("JINN_WEB_RENDER_MAX_CONCURRENCY"), RenderMaxRequests: webIntEnv("JINN_WEB_RENDER_MAX_REQUESTS"), RenderMaxNetworkBytes: webInt64Env("JINN_WEB_RENDER_MAX_NETWORK_BYTES"), RenderMaxHTMLBytes: webInt64Env("JINN_WEB_RENDER_MAX_HTML_BYTES"), ChromePath: strings.TrimSpace(os.Getenv("JINN_WEB_CHROME_PATH")),
	}
}

func defaultJinnWebCacheDir() string {
	if cacheHome := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cacheHome != "" {
		return filepath.Join(cacheHome, "jinn", "web", "urls")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "jinn", "web", "urls")
}
func webDurationEnv(name string) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	duration, err := time.ParseDuration(value)
	if value == "" || err != nil || duration < 0 {
		return 0
	}
	return duration
}
func webIntEnv(name string) int {
	value := webInt64Env(name)
	if value > int64(^uint(0)>>1) {
		return 0
	}
	return int(value)
}
func webInt64Env(name string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}
func webBoolEnv(name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && value
}

//nolint:goconst // Help spellings are part of the web subcommand grammar.
func runWeb(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("web requires a subcommand: fetch or search")
	}
	switch args[0] {
	case "fetch":
		return runWebFetch(ctx, args[1:])
	case "search":
		return runWebSearch(ctx, args[1:])
	case "--help", "-h", "help":
		_, err := fmt.Fprint(os.Stdout, webHelp)
		return err
	default:
		return fmt.Errorf("unknown web subcommand %q: use fetch or search", args[0])
	}
}

const webHelp = "Usage:\n  jinn web fetch [flags] URL\n  jinn web search [flags] QUERY\n\nUse --help after either subcommand for its flags. Web configuration uses JINN_WEB_* variables plus JINA_API_KEY, BRAVE_API_KEY, and EXA_API_KEY.\n"

func runWebFetch(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("jinn web fetch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	raw := flags.Bool("raw", false, "fetch response content directly")
	asJSON := flags.Bool("json", false, "emit flat JSON")
	reader := flags.String("reader", "", "reader: jina, defuddle, or auto")
	render := flags.String("render", "never", "render policy: never, auto, or always")
	renderWait := flags.String("render-wait", "", "render wait: load or networkidle")
	maxBytes := flags.Int("max-bytes", 0, "maximum output bytes")
	maxLines := flags.Int("max-lines", 0, "maximum output lines")
	cacheTTL := flags.String("cache-ttl", "", "reader cache TTL")
	cacheDir := flags.String("cache-dir", "", "reader cache directory")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		flags.SetOutput(os.Stdout)
		flags.PrintDefaults()
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("jinn web fetch requires exactly one URL")
	}
	limits, err := newWebOutputLimits(*maxBytes, *maxLines)
	if err != nil {
		return webCLIError{err: err, asJSON: *asJSON}
	}
	cfg := webConfig()
	if *cacheTTL != "" {
		cfg.URLCacheTTL, err = parseWebCacheTTL(*cacheTTL)
		if err != nil {
			return webCLIError{err: err, asJSON: *asJSON}
		}
	}
	if *cacheDir != "" {
		cfg.URLCacheDir = *cacheDir
	}
	service := webfetch.NewService(cfg)
	defer closeWebService(service)
	doc, err := service.Fetch(ctx, webfetch.FetchRequest{URL: flags.Arg(0), Raw: *raw, Reader: *reader, Render: *render, RenderWait: *renderWait})
	if err != nil {
		return webCLIError{err: err, asJSON: *asJSON}
	}
	return renderWebFetch(os.Stdout, doc, *asJSON, limits)
}

func runWebSearch(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("jinn web search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asJSON := flags.Bool("json", false, "emit flat JSON")
	provider := flags.String("provider", "", "search provider: brave or exa")
	limit := flags.Int("limit", webfetch.DefaultSearchLimit, "maximum results")
	category := flags.String("category", "", "provider category")
	var domains webStringSlice
	flags.Var(&domains, "include-domain", "hostname filter; may be repeated")
	startDate := flags.String("start-published-date", "", "RFC3339 or YYYY-MM-DD lower bound")
	highlights := flags.Bool("include-highlights", false, "request provider highlights")
	highlightSentences := flags.Int("highlight-sentences", 0, "number of highlight sentences")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		flags.SetOutput(os.Stdout)
		flags.PrintDefaults()
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("jinn web search requires exactly one query")
	}
	cfg := webConfig()
	if *provider != "" {
		cfg.SearchProvider = *provider
	}
	service := webfetch.NewService(cfg)
	defer closeWebService(service)
	result, err := service.Search(ctx, webfetch.SearchRequest{Query: flags.Arg(0), Limit: *limit, Category: *category, IncludeDomains: domains, StartPublishedDate: *startDate, IncludeHighlights: *highlights, HighlightSentences: *highlightSentences})
	if err != nil {
		return webCLIError{err: err, asJSON: *asJSON}
	}
	return renderWebSearch(os.Stdout, result, *asJSON)
}

type webStringSlice []string

func (values *webStringSlice) String() string         { return strings.Join(*values, ",") }
func (values *webStringSlice) Set(value string) error { *values = append(*values, value); return nil }

type webCLIError struct {
	err    error
	asJSON bool
}

func (e webCLIError) Error() string { return e.err.Error() }
func closeWebService(service *webfetch.Service) {
	closeCtx, cancel := context.WithTimeout(context.Background(), webCloseTimeout)
	defer cancel()
	_ = service.Close(closeCtx)
}
func parseWebCacheTTL(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" || value == "0" || value == "0s" {
		return 0, nil
	}
	ttl, err := time.ParseDuration(value)
	if err != nil || ttl < 0 {
		return 0, fmt.Errorf("invalid cache TTL %q: use a non-negative Go duration", value)
	}
	return ttl, nil
}

type webOutputLimits = webfetch.OutputLimits

func newWebOutputLimits(maxBytes, maxLines int) (webOutputLimits, error) {
	return webfetch.NewOutputLimits(maxBytes, maxLines)
}
func projectWebContent(content string, limits webOutputLimits) string {
	return webfetch.ProjectContent(content, limits).Content
}

//nolint:nestif // Human truncation formatting is clearer as one local branch.
func renderWebFetch(out io.Writer, doc webfetch.Document, asJSON bool, limits webOutputLimits) error {
	projection := webfetch.ProjectContent(doc.Content, limits)
	if !asJSON {
		if _, err := io.WriteString(out, projection.Content); err != nil {
			return err
		}
		if !projection.Truncated {
			return nil
		}
		if projection.Content != "" && !strings.HasSuffix(projection.Content, "\n") {
			if _, err := io.WriteString(out, "\n"); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(out, "%s\n", webfetch.TruncationMarker(projection))
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		OK          bool     `json:"ok"`
		Mode        string   `json:"mode"`
		URL         string   `json:"url"`
		FinalURL    string   `json:"final_url,omitempty"`
		StatusCode  int      `json:"status_code"`
		ContentType string   `json:"content_type,omitempty"`
		Title       string   `json:"title,omitempty"`
		Description string   `json:"description,omitempty"`
		Domain      string   `json:"domain,omitempty"`
		Favicon     string   `json:"favicon,omitempty"`
		Image       string   `json:"image,omitempty"`
		Language    string   `json:"language,omitempty"`
		Published   string   `json:"published,omitempty"`
		Author      string   `json:"author,omitempty"`
		Site        string   `json:"site,omitempty"`
		WordCount   int      `json:"word_count,omitempty"`
		Extractor   string   `json:"extractor,omitempty"`
		Rendered    bool     `json:"rendered,omitempty"`
		Warnings    []string `json:"warnings,omitempty"`
		Content     string   `json:"content"`
		Truncated   bool     `json:"truncated,omitempty"`
		TruncatedBy string   `json:"truncated_by,omitempty"`
		TotalBytes  int      `json:"total_bytes,omitempty"`
		OutputBytes int      `json:"output_bytes,omitempty"`
		TotalLines  int      `json:"total_lines,omitempty"`
		OutputLines int      `json:"output_lines,omitempty"`
		MaxBytes    int      `json:"max_bytes,omitempty"`
		MaxLines    int      `json:"max_lines,omitempty"`
	}{
		OK: true, Mode: doc.Source, URL: doc.URL, FinalURL: doc.FinalURL,
		StatusCode: doc.StatusCode, ContentType: doc.ContentType, Title: doc.Title,
		Description: doc.Description, Domain: doc.Domain, Favicon: doc.Favicon,
		Image: doc.Image, Language: doc.Language, Published: doc.Published,
		Author: doc.Author, Site: doc.Site, WordCount: doc.WordCount,
		Extractor: doc.Extractor, Rendered: doc.Rendered, Warnings: doc.Warnings,
		Content: projection.Content, Truncated: projection.Truncated,
		TruncatedBy: projection.TruncatedBy, TotalBytes: projection.TotalBytes,
		OutputBytes: projection.OutputBytes, TotalLines: projection.TotalLines,
		OutputLines: projection.OutputLines, MaxBytes: limits.MaxBytes, MaxLines: limits.MaxLines,
	})
}
func renderWebSearch(out io.Writer, result webfetch.SearchResponse, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(struct {
			OK      bool                    `json:"ok"`
			Query   string                  `json:"query"`
			Results []webfetch.SearchResult `json:"results"`
		}{true, result.Query, result.Results})
	}
	if len(result.Results) == 0 {
		_, err := fmt.Fprintln(out, "No results.")
		return err
	}
	for index, hit := range result.Results {
		if _, err := fmt.Fprintf(out, "%d. %s\n   %s\n", index+1, hit.Title, hit.URL); err != nil {
			return err
		}
		if hit.Description != "" {
			if _, err := fmt.Fprintf(out, "   %s\n", hit.Description); err != nil {
				return err
			}
		}
	}
	return nil
}
func renderWebJSONError(err error) error {
	output := struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		ErrorCode  string `json:"error_code,omitempty"`
		Suggestion string `json:"suggestion,omitempty"`
	}{OK: false, Error: err.Error()}
	var coded *webfetch.CodedError
	if errors.As(err, &coded) {
		output.ErrorCode, output.Suggestion = coded.Code, coded.Suggestion
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}
