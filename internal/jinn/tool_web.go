package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dotcommander/jinn/internal/webfetch"
)

const (
	webFetchTool  = "web_fetch"
	webSearchTool = "web_search"
)

// webService returns the engine-owned lazy service. Service construction is
// side-effect free; browser startup remains lazy inside the renderer.
func (e *Engine) webService() *webfetch.Service {
	e.webMu.Lock()
	defer e.webMu.Unlock()
	if e.web == nil {
		e.web = webfetch.NewService(e.webConfig)
	}
	return e.web
}

func (e *Engine) dispatchWeb(ctx context.Context, args map[string]any, tool string) (*ToolResult, bool, error) {
	switch tool {
	case webFetchTool:
		req := webfetch.FetchRequest{URL: stringArg(args, "url")}
		req.Raw, _ = args["raw"].(bool)
		req.Reader = stringArg(args, "reader")
		req.Render = stringArg(args, "render")
		req.RenderWait = stringArg(args, "render_wait")
		format := stringArg(args, "format")
		if req.Raw && !webfetch.ProjectionFormatIsMarkdown(format) {
			return nil, true, mapWebError(webfetch.NewCodedError(errors.New("raw is only compatible with format markdown"), webfetch.ErrorCodeInvalidArgument, "omit format or use format markdown with raw content"))
		}
		startLine := intArg(args, "start_line", 0)
		limits, err := webfetch.NewOutputLimits(intArg(args, "max_bytes", 0), intArg(args, "max_lines", 0))
		if err != nil {
			return nil, true, mapWebError(err)
		}
		doc, err := e.webService().Fetch(ctx, req)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		projection, err := webfetch.ProjectFetchContent(doc.Content, format, startLine, limits)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		return &ToolResult{Text: projection.Content, Meta: webFetchMeta(doc, projection, limits)}, true, nil
	case webSearchTool:
		req, err := webfetch.ParseSearchRequest(args)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		result, err := e.webService().Search(ctx, req)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		text, err := json.Marshal(struct {
			Query   string                  `json:"query"`
			Results []webfetch.SearchResult `json:"results"`
		}{Query: result.Query, Results: result.Results})
		if err != nil {
			return nil, true, fmt.Errorf("encode web search result: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"provider": result.Provider, "count": len(result.Results)}}, true, nil
	default:
		return nil, false, nil
	}
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func webFetchMeta(doc webfetch.Document, projection webfetch.ContentProjection, limits webfetch.OutputLimits) map[string]any {
	return map[string]any{
		"url": doc.URL, "final_url": doc.FinalURL, "status_code": doc.StatusCode,
		"content_type": doc.ContentType, "title": doc.Title, "description": doc.Description,
		"domain": doc.Domain, "favicon": doc.Favicon, "image": doc.Image,
		"language": doc.Language, "published": doc.Published, "author": doc.Author,
		"site": doc.Site, "word_count": doc.WordCount, "extractor": doc.Extractor,
		"source": doc.Source, "rendered": doc.Rendered, "warnings": doc.Warnings,
		"format": projection.Format, "truncated": projection.Truncated, "truncated_by": projection.TruncatedBy,
		"total_bytes": projection.TotalBytes, "output_bytes": projection.OutputBytes,
		"total_lines": projection.TotalLines, "output_lines": projection.OutputLines,
		"max_bytes": limits.MaxBytes, "max_lines": limits.MaxLines,
		"start_line": projection.StartLine, "next_start_line": projection.NextStartLine,
	}
}

func mapWebError(err error) error {
	var coded *webfetch.CodedError
	if errors.As(err, &coded) {
		return &ErrWithSuggestion{Err: coded, Suggestion: coded.Suggestion, Code: coded.Code}
	}
	return err
}
