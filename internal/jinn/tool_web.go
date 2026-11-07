package jinn

import (
	"context"
	"encoding/base64"
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
		startLineByte := intArg(args, "start_line_byte", 0)
		limits, err := webfetch.NewOutputLimits(intArg(args, "max_bytes", 0), intArg(args, "max_lines", 0))
		if err != nil {
			return nil, true, mapWebError(err)
		}
		doc, err := e.webService().Fetch(ctx, req)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		if doc.Source == "image" {
			return webImageResult(doc), true, nil
		}
		if stringArg(args, "mode") == "extract" {
			return dispatchWebExtract(doc, args)
		}
		if view := webFetchView(args); view != "" {
			return dispatchWebView(doc, args, view)
		}
		projection, err := webfetch.ProjectFetchContentAt(doc.Content, format, startLine, startLineByte, limits)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		return &ToolResult{Text: webfetch.SanitizeText(projection.Content), Meta: webFetchMeta(doc, projection, limits)}, true, nil
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
			Query     string                    `json:"query"`
			Results   []webfetch.SearchResult   `json:"results"`
			Fallbacks []webfetch.SearchFallback `json:"fallbacks,omitempty"`
		}{Query: result.Query, Results: result.Results, Fallbacks: result.Fallbacks})
		if err != nil {
			return nil, true, fmt.Errorf("encode web search result: %w", err)
		}
		meta := map[string]any{"provider": result.Provider, "count": len(result.Results), "combine": result.Combine}
		if len(result.Fallbacks) > 0 {
			meta["fallbacks"] = result.Fallbacks
		}
		return &ToolResult{Text: string(text), Meta: meta}, true, nil
	default:
		return nil, false, nil
	}
}

// dispatchWebExtract serves web_fetch mode="extract": typed fields are
// grounded line by line in the fetched document, returning the value
// with its source line, or an explicit not_on_page/uncertain result.
// The page content itself is never returned.
func dispatchWebExtract(doc webfetch.Document, args map[string]any) (*ToolResult, bool, error) {
	fieldsArg, ok := args["fields"].(map[string]any)
	if !ok || len(fieldsArg) == 0 {
		return nil, true, mapWebError(webfetch.NewCodedError(errors.New("fields are required for extract mode"), webfetch.ErrorCodeInvalidArgument, "provide fields as a map of name to type (string, number, boolean)"))
	}
	fields := make(map[string]string, len(fieldsArg))
	for name, rawType := range fieldsArg {
		fieldType, ok := rawType.(string)
		if !ok || !webfetch.ValidFieldType(fieldType) {
			return nil, true, mapWebError(webfetch.NewCodedError(fmt.Errorf("unsupported type %v for field %q", rawType, name), webfetch.ErrorCodeInvalidArgument, "use string, number, or boolean"))
		}
		fields[name] = fieldType
	}
	results := webfetch.ExtractFields(doc.Content, fields)
	text, err := json.Marshal(struct {
		URL    string                 `json:"url"`
		Fields []webfetch.FieldResult `json:"fields"`
	}{URL: doc.FinalURL, Fields: results})
	if err != nil {
		return nil, true, fmt.Errorf("encode extraction result: %w", err)
	}
	return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "extracted": len(results), "mode": "extract"}}, true, nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func webImageResult(doc webfetch.Document) *ToolResult {
	text := fmt.Sprintf("Image fetched: %s (%s, %d bytes)", doc.FinalURL, doc.ImageMimeType, len(doc.ImageBytes))
	meta := map[string]any{
		"url": doc.URL, "final_url": doc.FinalURL, "status_code": doc.StatusCode,
		"content_type": doc.ContentType, "source": doc.Source,
		"image_mime_type": doc.ImageMimeType, "image_bytes": len(doc.ImageBytes),
		"converter": "image",
	}
	return &ToolResult{
		Text: text,
		Content: []ContentBlock{{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(doc.ImageBytes),
			MimeType: doc.ImageMimeType,
		}},
		Meta: meta,
	}
}

func webFetchMeta(doc webfetch.Document, projection webfetch.ContentProjection, limits webfetch.OutputLimits) map[string]any {
	return map[string]any{
		"url": doc.URL, "final_url": doc.FinalURL, "status_code": doc.StatusCode,
		"content_type": doc.ContentType, "title": doc.Title, "description": doc.Description,
		"domain": doc.Domain, "favicon": doc.Favicon, "image": doc.Image,
		"language": doc.Language, "published": doc.Published, "author": doc.Author,
		"site": doc.Site, "word_count": doc.WordCount, "extractor": doc.Extractor,
		"source": doc.Source, "rendered": doc.Rendered, "warnings": doc.Warnings, "quality": doc.Quality,
		"format": projection.Format, "truncated": projection.Truncated, "truncated_by": projection.TruncatedBy,
		"total_bytes": projection.TotalBytes, "output_bytes": projection.OutputBytes,
		"total_lines": projection.TotalLines, "output_lines": projection.OutputLines,
		"max_bytes": limits.MaxBytes, "max_lines": limits.MaxLines,
		"start_line": projection.StartLine, "next_start_line": projection.NextStartLine,
		"start_line_byte": projection.StartLineByte, "next_start_line_byte": projection.NextStartLineByte,
	}
}

func mapWebError(err error) error {
	var coded *webfetch.CodedError
	if errors.As(err, &coded) {
		return &ErrWithSuggestion{Err: coded, Suggestion: coded.Suggestion, Code: coded.Code}
	}
	return err
}

// webFetchView selects one projection of the fetched document:
// "toc", "section", or "must_contain" (inline modes, not extract fields).
func webFetchView(args map[string]any) string {
	if toc, _ := args["toc"].(bool); toc {
		return "toc"
	}
	if section := stringArg(args, "section"); section != "" {
		return "section"
	}
	if _, present := args["must_contain"]; present {
		return "must_contain"
	}
	return ""
}

// dispatchWebView serves the toc/section/must_contain projections. Each is a
// pure deterministic function of the document markdown; full content is never
// returned for must_contain.
func dispatchWebView(doc webfetch.Document, args map[string]any, view string) (*ToolResult, bool, error) {
	switch view {
	case "toc":
		sections := webfetch.DocumentOutline(doc.Content)
		text, err := json.Marshal(struct {
			Sections []webfetch.SectionInfo `json:"sections"`
		}{Sections: sections})
		if err != nil {
			return nil, true, fmt.Errorf("encode toc view: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "view": "toc", "sections": len(sections)}}, true, nil
	case "section":
		sectionID := stringArg(args, "section")
		sections := webfetch.DocumentOutline(doc.Content)
		totalLines := len(splitLines(doc.Content))
		start, end, found := webfetch.SectionRange(sections, sectionID, totalLines)
		if !found {
			return nil, true, mapWebError(webfetch.NewCodedError(fmt.Errorf("unknown section %q", sectionID), webfetch.ErrorCodeInvalidArgument, "use a section id from the toc view"))
		}
		text, err := json.Marshal(struct {
			Section string `json:"section"`
			Start   int    `json:"start_line"`
			End     int    `json:"end_line"`
			Content string `json:"content"`
		}{Section: sectionID, Start: start, End: end, Content: joinLines(splitLines(doc.Content)[start:end])})
		if err != nil {
			return nil, true, fmt.Errorf("encode section view: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "view": "section", "section": sectionID}}, true, nil
	case "must_contain":
		terms, err := stringSliceArg(args, "must_contain")
		if err != nil {
			return nil, true, mapWebError(err)
		}
		clean, err := webfetch.MustContainTerms(terms)
		if err != nil {
			return nil, true, mapWebError(err)
		}
		hits := make([]webfetch.ProbeHit, 0, len(clean))
		for _, term := range clean {
			hit, err := webfetch.ProbeDocument(doc.Content, term)
			if err != nil {
				return nil, true, mapWebError(err)
			}
			hits = append(hits, hit)
		}
		text, err := json.Marshal(struct {
			Hits []webfetch.ProbeHit `json:"hits"`
		}{Hits: hits})
		if err != nil {
			return nil, true, fmt.Errorf("encode must_contain view: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "view": "must_contain", "terms": len(clean)}}, true, nil
	default:
		return nil, true, mapWebError(webfetch.NewCodedError(fmt.Errorf("unsupported view %q", view), webfetch.ErrorCodeInvalidArgument, "use toc, section, or must_contain"))
	}
}

func stringSliceArg(args map[string]any, key string) ([]string, error) {
	raw, present := args[key]
	if !present {
		return nil, webfetch.NewCodedError(errors.New("must_contain is required"), webfetch.ErrorCodeInvalidArgument, "provide must_contain as a list of probe terms")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, webfetch.NewCodedError(fmt.Errorf("%s must be a list of strings", key), webfetch.ErrorCodeInvalidArgument, "provide must_contain as a list of probe terms")
	}
	terms := make([]string, 0, len(list))
	for _, item := range list {
		term, ok := item.(string)
		if !ok {
			return nil, webfetch.NewCodedError(fmt.Errorf("%s must be a list of strings", key), webfetch.ErrorCodeInvalidArgument, "provide must_contain as a list of probe terms")
		}
		terms = append(terms, term)
	}
	return terms, nil
}
