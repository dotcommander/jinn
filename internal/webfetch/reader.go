package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type readerProvider struct {
	client   *Client
	endpoint string
	apiKey   string
}

//nolint:revive // Grouped exported constants are one reader-mode contract.
const (
	ReaderModeJina     = "jina"
	ReaderModeDefuddle = "defuddle"
	ReaderModeAuto     = "auto"
)

const jinaResponseSuggestion = "retry the Jina reader or use -reader defuddle"

//nolint:gochecknoglobals // Immutable provider failure prefixes are shared response policy.
var jinaErrorPrefixes = []string{
	"error:",
	"unable to",
	"javascript is not available",
	"please enable javascript",
	"access denied",
	"page not found",
	"403 forbidden",
	"404 not found",
}

func (p readerProvider) fetch(ctx context.Context, target *url.URL) (Document, error) {
	if err := p.probeRedirects(ctx, target); err != nil {
		return Document{}, err
	}
	readerURL := strings.TrimRight(p.endpoint, "/") + "/" + target.String()
	headers := make(http.Header)
	headers.Set("Accept", contentTypeMarkdown)
	headers.Set("X-Return-Format", "markdown")
	if strings.TrimSpace(p.apiKey) != "" {
		headers.Set(headerAuthorization, "Bearer "+strings.TrimSpace(p.apiKey))
	}
	resp, err := p.client.Get(ctx, readerURL, headers)
	if err != nil {
		return Document{}, fmt.Errorf("reader fetch: %w", err)
	}
	if err := validateJinaResponse(resp); err != nil {
		return Document{}, fmt.Errorf("reader response: %w", err)
	}
	if err := checkJinaSourceURL(target, string(resp.Body)); err != nil {
		return Document{}, err
	}
	return Document{
		URL:         target.String(),
		FinalURL:    resp.URL,
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Title:       markdownTitle(string(resp.Body)),
		Source:      sourceReader,
		Quality:     QualityOf(string(resp.Body)),
		Content:     string(resp.Body),
	}, nil
}

func (p readerProvider) fetchAuto(ctx context.Context, target *url.URL) (Document, error) {
	doc, defuddleErr := p.fetchDefuddle(ctx, target)
	if defuddleErr == nil {
		return doc, nil
	}
	if ctx.Err() != nil {
		return Document{}, codeContextError(ctx.Err())
	}
	var coded *CodedError
	if errors.As(defuddleErr, &coded) && coded.Code == ErrorCodeCrossHostRedirect {
		// A cross-host redirect is a policy stop, not a reader failure:
		// falling back to the hosted reader would follow it silently.
		return Document{}, defuddleErr
	}

	doc, jinaErr := p.fetch(ctx, target)
	if jinaErr == nil {
		return doc, nil
	}
	if ctx.Err() != nil {
		return Document{}, codeContextError(ctx.Err())
	}
	return Document{}, newCodedError(
		fmt.Errorf("auto reader failed: defuddle: %w; jina: %w", defuddleErr, jinaErr),
		ErrorCodeReaderFallback,
		"use -reader jina, -reader defuddle, or -raw to choose one path",
	)
}

// probeRedirects resolves the target's redirect chain through the shared
// client (which stops at the first cross-host hop) before a hosted read.
// A cross-host redirect is a policy stop; any other probe outcome
// (including servers that refuse HEAD) is inconclusive and the hosted
// reader proceeds, since the probe must not become a second fetch gate.
func (p readerProvider) probeRedirects(ctx context.Context, target *url.URL) error {
	headers := make(http.Header)
	headers.Set("Accept", "*/*")
	_, err := p.client.Do(ctx, http.MethodHead, target.String(), headers, nil)
	if err == nil {
		return nil
	}
	var coded *CodedError
	if errors.As(err, &coded) && coded.Code == ErrorCodeCrossHostRedirect {
		return err
	}
	return nil
}

// checkJinaSourceURL rejects hosted-read content whose reported source URL
// points at a different host than requested. Absent or malformed metadata
// is accepted: it cannot prove a host change.
func checkJinaSourceURL(target *url.URL, content string) error {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "URL Source:") {
			continue
		}
		source, err := url.Parse(strings.TrimSpace(strings.TrimPrefix(line, "URL Source:")))
		if err != nil {
			return nil
		}
		if !sameHost(target, source) {
			return newCodedError(
				fmt.Errorf("hosted reader source is a different host: %s", source),
				ErrorCodeCrossHostRedirect,
				"fetch the redirected URL explicitly to confirm the new host",
			)
		}
	}
	return nil
}

func validateJinaResponse(resp httpResponse) error {
	if message := strings.TrimSpace(resp.Header.Get("X-Respond-Error")); message != "" {
		return newCodedError(
			fmt.Errorf("reader provider error: %s", message),
			ErrorCodeProviderResponse,
			jinaResponseSuggestion,
		)
	}

	trimmed := strings.TrimSpace(string(resp.Body))
	check := strings.ToLower(trimmed)
	if len(check) > 500 {
		check = check[:500]
	}
	for _, prefix := range jinaErrorPrefixes {
		if strings.HasPrefix(check, prefix) {
			return newCodedError(
				fmt.Errorf("reader provider error: %s", truncate(trimmed, 100)),
				ErrorCodeProviderResponse,
				jinaResponseSuggestion,
			)
		}
	}
	return nil
}

func markdownTitle(markdown string) string {
	lines := strings.SplitN(markdown, "\n", 11)
	metadataLines := lines
	if len(metadataLines) > 10 {
		metadataLines = metadataLines[:10]
	}
	for _, line := range metadataLines {
		line = strings.TrimSpace(line)
		if title, ok := strings.CutPrefix(line, "Title:"); ok {
			if title = strings.TrimSpace(title); title != "" {
				return title
			}
		}
	}

	for markdown != "" {
		line, rest, _ := strings.Cut(markdown, "\n")
		markdown = rest
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}
