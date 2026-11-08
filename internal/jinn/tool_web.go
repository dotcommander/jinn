package jinn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dotcommander/jinn/internal/webfetch"
)

const (
	webFetchTool    = "web_fetch"
	webSearchTool   = "web_search"
	webContentTool  = "web_content"
	sourceCheckTool = "source_check"
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

//nolint:revive // thin router; one case per web tool
func (e *Engine) dispatchWeb(ctx context.Context, args map[string]any, tool string) (*ToolResult, bool, error) {
	switch tool {
	case webFetchTool:
		result, err := e.dispatchWebFetch(ctx, args)
		return result, true, err
	case webSearchTool:
		result, err := e.dispatchWebSearch(ctx, args)
		return result, true, err
	case webContentTool:
		return e.dispatchWebContent(args)
	case sourceCheckTool:
		return e.dispatchSourceCheck(ctx, args)
	default:
		return nil, false, nil
	}
}

func (e *Engine) dispatchWebFetch(ctx context.Context, args map[string]any) (*ToolResult, error) {
	req := webfetch.FetchRequest{URL: stringArg(args, "url")}
	req.Raw, _ = args["raw"].(bool)
	req.Reader = stringArg(args, "reader")
	req.Render = stringArg(args, "render")
	req.RenderWait = stringArg(args, "render_wait")
	format := stringArg(args, "format")
	if req.Raw && !webfetch.ProjectionFormatIsMarkdown(format) {
		return nil, mapWebError(webfetch.NewCodedError(errors.New("raw is only compatible with format markdown"), webfetch.ErrorCodeInvalidArgument, "omit format or use format markdown with raw content"))
	}
	startLine := intArg(args, "start_line", 0)
	startLineByte := intArg(args, "start_line_byte", 0)
	limits, err := webfetch.NewOutputLimits(intArg(args, "max_bytes", 0), intArg(args, "max_lines", 0))
	if err != nil {
		return nil, mapWebError(err)
	}
	doc, err := e.webService().Fetch(ctx, req)
	if err != nil {
		return nil, mapWebError(err)
	}
	if doc.Source == "image" {
		return webImageResult(doc), nil
	}
	meta := map[string]any{"outcome": doc.Outcome}
	if persist, _ := args["persist_snapshot"].(bool); persist {
		meta["snapshot"] = e.webService().SaveDocumentSnapshot(doc)
	}
	if stringArg(args, "mode") == "extract" {
		result, extractErr := dispatchWebExtract(doc, args)
		if extractErr == nil && result != nil {
			result.Meta = mergeMeta(result.Meta, meta)
		}
		return result, extractErr
	}
	if view := webFetchView(args); view != "" {
		result, viewErr := dispatchWebView(doc, args, view)
		if viewErr == nil && result != nil {
			result.Meta = mergeMeta(result.Meta, meta)
		}
		return result, viewErr
	}
	projection, err := webfetch.ProjectFetchContentAt(doc.Content, format, startLine, startLineByte, limits)
	if err != nil {
		return nil, mapWebError(err)
	}
	return &ToolResult{Text: webfetch.SanitizeText(projection.Content), Meta: mergeMeta(webFetchMeta(doc, projection, limits), meta)}, nil
}

func (e *Engine) dispatchWebSearch(ctx context.Context, args map[string]any) (*ToolResult, error) {
	req, err := webfetch.ParseSearchRequest(args)
	if err != nil {
		return nil, mapWebError(err)
	}
	result, err := e.webService().Search(ctx, req)
	if err != nil {
		return nil, mapWebError(err)
	}
	text, err := json.Marshal(struct {
		Query     string                        `json:"query"`
		Provider  string                        `json:"provider,omitempty"`
		Results   []webfetch.SearchResult       `json:"results"`
		Fallbacks []webfetch.SearchFallback     `json:"fallbacks,omitempty"`
		Queries   []webfetch.SearchQueryOutcome `json:"queries,omitempty"`
		Summary   *webfetch.SearchSummary       `json:"summary,omitempty"`
	}{Query: result.Query, Provider: result.Provider, Results: result.Results, Fallbacks: result.Fallbacks, Queries: result.Queries, Summary: result.Summary})
	if err != nil {
		return nil, fmt.Errorf("encode web search result: %w", err)
	}
	meta := map[string]any{"provider": result.Provider, "count": len(result.Results), "combine": result.Combine}
	if len(result.Fallbacks) > 0 {
		meta["fallbacks"] = result.Fallbacks
	}
	if len(result.Queries) > 0 {
		meta["queries"] = len(result.Queries)
	}
	if persist, _ := args["persist_snapshot"].(bool); persist {
		meta["snapshot"] = e.webService().SaveSearchSnapshot(result)
	}
	return &ToolResult{Text: string(text), Meta: meta}, nil
}

// mergeMeta overlays extra metadata (outcome, snapshot) onto a result's
// own metadata without overwriting its keys.
func mergeMeta(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	return merged
}

// optionalStringSliceArg reads an optional string-list argument.
func optionalStringSliceArg(args map[string]any, key string) ([]string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, webfetch.NewCodedError(fmt.Errorf("%s must be a list of strings", key), webfetch.ErrorCodeInvalidArgument, fmt.Sprintf("provide %s as a list of strings", key))
	}
	values := make([]string, 0, len(list))
	for _, item := range list {
		value, ok := item.(string)
		if !ok {
			return nil, webfetch.NewCodedError(fmt.Errorf("%s must be a list of strings", key), webfetch.ErrorCodeInvalidArgument, fmt.Sprintf("provide %s as a list of strings", key))
		}
		values = append(values, value)
	}
	return values, nil
}

// webContentSearchResult serves one search snapshot: optional item
// selection, optional find_text filtering, then JSON encoding.
func webContentSearchResult(snap webfetch.Snapshot, meta map[string]any, args map[string]any, findText string) (*ToolResult, error) {
	results := snap.Results
	if item, present := args["item"]; present && item != nil {
		index, itemErr := parseSnapshotItem(item)
		if itemErr != nil {
			return nil, mapWebError(itemErr)
		}
		if index < 0 || index >= len(results) {
			return nil, mapWebError(webfetch.NewCodedError(fmt.Errorf("snapshot item %d out of range (0-%d)", index, len(results)-1), webfetch.ErrorCodeInvalidArgument, "omit item or choose an index within the snapshot results"))
		}
		results = results[index : index+1]
	}
	if findText != "" {
		results = filterSearchResultsByText(results, findText)
		meta["find_text"] = findText
		meta["matches"] = len(results)
	}
	text, marshalErr := json.Marshal(struct {
		Query     string                        `json:"query"`
		Provider  string                        `json:"provider,omitempty"`
		Results   []webfetch.SearchResult       `json:"results"`
		Fallbacks []webfetch.SearchFallback     `json:"fallbacks,omitempty"`
		Queries   []webfetch.SearchQueryOutcome `json:"queries,omitempty"`
	}{Query: snap.Query, Provider: snap.Provider, Results: results, Fallbacks: snap.Fallbacks, Queries: snap.Queries})
	if marshalErr != nil {
		return nil, fmt.Errorf("encode snapshot search result: %w", marshalErr)
	}
	meta["count"] = len(results)
	return &ToolResult{Text: string(text), Meta: meta}, nil
}

// webContentDocumentResult serves one document snapshot: optional
// find_text matching, otherwise paginated projection.
func webContentDocumentResult(snap webfetch.Snapshot, meta map[string]any, args map[string]any, findText string) (*ToolResult, error) {
	if snap.Document == nil {
		return nil, mapWebError(webfetch.NewCodedError(errors.New("snapshot document is missing"), webfetch.ErrorCodeSnapshotInvalid, "re-run the fetch with persist_snapshot"))
	}
	doc := *snap.Document
	meta["url"] = doc.URL
	meta["final_url"] = doc.FinalURL
	meta["title"] = doc.Title
	meta["outcome"] = doc.Outcome
	if findText != "" {
		lines, excerpts := findTextInContent(doc.Content, findText)
		meta["find_text"] = findText
		meta["matches"] = len(lines)
		meta["match_lines"] = lines
		return &ToolResult{Text: strings.Join(excerpts, "\n"), Meta: meta}, nil
	}
	startLine := intArg(args, "start_line", 0)
	startLineByte := intArg(args, "start_line_byte", 0)
	limits, limitsErr := webfetch.NewOutputLimits(intArg(args, "max_bytes", 0), intArg(args, "max_lines", 0))
	if limitsErr != nil {
		return nil, mapWebError(limitsErr)
	}
	projection, projErr := webfetch.ProjectFetchContentAt(doc.Content, "markdown", startLine, startLineByte, limits)
	if projErr != nil {
		return nil, mapWebError(projErr)
	}
	meta["truncated"] = projection.Truncated
	meta["next_start_line"] = projection.NextStartLine
	meta["next_start_line_byte"] = projection.NextStartLineByte
	meta["total_lines"] = projection.TotalLines
	meta["output_lines"] = projection.OutputLines
	return &ToolResult{Text: webfetch.SanitizeText(projection.Content), Meta: meta}, nil
}

// dispatchWebContent serves web_content: verbatim retrieval of a durable
// snapshot (search results or a fetched document) without refetching.
func (e *Engine) dispatchWebContent(args map[string]any) (*ToolResult, bool, error) {
	snapshotID := stringArg(args, "snapshot_id")
	if snapshotID == "" {
		return nil, true, mapWebError(webfetch.NewCodedError(errors.New("snapshot_id is required"), webfetch.ErrorCodeInvalidArgument, "pass the snapshot id from a previous persist_snapshot result"))
	}
	snap, err := e.webService().GetSnapshot(snapshotID)
	if err != nil {
		return nil, true, mapWebError(err)
	}
	meta := map[string]any{
		"snapshot_id": snap.ID, "kind": snap.Kind, "digest": snap.Digest,
		"created_at": snap.CreatedAt.Format(timeRFC3339), "expires_at": snap.ExpiresAt.Format(timeRFC3339),
	}
	findText := stringArg(args, "find_text")
	switch snap.Kind {
	case "search":
		result, searchErr := webContentSearchResult(snap, meta, args, findText)
		return result, true, searchErr
	case "document":
		result, docErr := webContentDocumentResult(snap, meta, args, findText)
		return result, true, docErr
	default:
		return nil, true, mapWebError(webfetch.NewCodedError(fmt.Errorf("unknown snapshot kind %q", snap.Kind), webfetch.ErrorCodeSnapshotInvalid, "re-run the search or fetch with persist_snapshot"))
	}
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// parseSnapshotItem accepts a zero-based result index from JSON numbers
// (intArg cannot express 0, which is a valid search-result index).
func parseSnapshotItem(raw any) (int, error) {
	switch value := raw.(type) {
	case float64:
		if value == float64(int(value)) && value >= 0 {
			return int(value), nil
		}
	case int:
		if value >= 0 {
			return value, nil
		}
	case int64:
		if value >= 0 {
			return int(value), nil
		}
	}
	return -1, webfetch.NewCodedError(errors.New("web_content: 'item' must be a non-negative integer"), webfetch.ErrorCodeInvalidArgument, "pass a zero-based result index")
}

func filterSearchResultsByText(results []webfetch.SearchResult, findText string) []webfetch.SearchResult {
	needle := strings.ToLower(findText)
	var matched []webfetch.SearchResult
	for _, result := range results {
		haystack := strings.ToLower(result.Title + " " + result.URL + " " + result.Description + " " + strings.Join(result.Highlights, " "))
		if strings.Contains(haystack, needle) {
			matched = append(matched, result)
		}
	}
	return matched
}

func findTextInContent(content, findText string) (lines []int, excerpts []string) {
	needle := strings.ToLower(findText)
	for lineNo, line := range strings.Split(content, "\n") {
		if !strings.Contains(strings.ToLower(line), needle) {
			continue
		}
		lines = append(lines, lineNo+1)
		trimmed := strings.TrimSpace(line)
		if len([]rune(trimmed)) > 200 {
			trimmed = string([]rune(trimmed)[:200]) + "…"
		}
		excerpts = append(excerpts, trimmed)
		if len(excerpts) >= 20 {
			break
		}
	}
	return lines, excerpts
}

// dispatchSourceCheck serves source_check: lexical term evidence over an
// explicit set of URLs or document snapshots. No semantic verdict.
func (e *Engine) dispatchSourceCheck(ctx context.Context, args map[string]any) (*ToolResult, bool, error) {
	urls, err := optionalStringSliceArg(args, "urls")
	if err != nil {
		return nil, true, mapWebError(err)
	}
	snapshotIDs, err := optionalStringSliceArg(args, "snapshot_ids")
	if err != nil {
		return nil, true, mapWebError(err)
	}
	terms, err := optionalStringSliceArg(args, "terms")
	if err != nil {
		return nil, true, mapWebError(err)
	}
	resp, err := e.webService().SourceCheck(ctx, webfetch.SourceCheckRequest{
		Claim: stringArg(args, "claim"), URLs: urls, SnapshotIDs: snapshotIDs, Terms: terms,
	})
	if err != nil {
		return nil, true, mapWebError(err)
	}
	text, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return nil, true, fmt.Errorf("encode source_check result: %w", marshalErr)
	}
	return &ToolResult{Text: string(text), Meta: map[string]any{"sources": len(resp.Sources)}}, true, nil
}

// dispatchWebExtract serves web_fetch mode="extract": typed fields are
// grounded line by line in the fetched document, returning the value
// with its source line, or an explicit not_on_page/uncertain result.
// The page content itself is never returned.
func dispatchWebExtract(doc webfetch.Document, args map[string]any) (*ToolResult, error) {
	fieldsArg, ok := args["fields"].(map[string]any)
	if !ok || len(fieldsArg) == 0 {
		return nil, mapWebError(webfetch.NewCodedError(errors.New("fields are required for extract mode"), webfetch.ErrorCodeInvalidArgument, "provide fields as a map of name to type (string, number, boolean)"))
	}
	fields := make(map[string]string, len(fieldsArg))
	for name, rawType := range fieldsArg {
		fieldType, ok := rawType.(string)
		if !ok || !webfetch.ValidFieldType(fieldType) {
			return nil, mapWebError(webfetch.NewCodedError(fmt.Errorf("unsupported type %v for field %q", rawType, name), webfetch.ErrorCodeInvalidArgument, "use string, number, or boolean"))
		}
		fields[name] = fieldType
	}
	results := webfetch.ExtractFields(doc.Content, fields)
	text, err := json.Marshal(struct {
		URL    string                 `json:"url"`
		Fields []webfetch.FieldResult `json:"fields"`
	}{URL: doc.FinalURL, Fields: results})
	if err != nil {
		return nil, fmt.Errorf("encode extraction result: %w", err)
	}
	return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "extracted": len(results), "mode": "extract"}}, nil
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
func dispatchWebView(doc webfetch.Document, args map[string]any, view string) (*ToolResult, error) {
	switch view {
	case "toc":
		sections := webfetch.DocumentOutline(doc.Content)
		text, err := json.Marshal(struct {
			Sections []webfetch.SectionInfo `json:"sections"`
		}{Sections: sections})
		if err != nil {
			return nil, fmt.Errorf("encode toc view: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "view": "toc", "sections": len(sections)}}, nil
	case "section":
		sectionID := stringArg(args, "section")
		sections := webfetch.DocumentOutline(doc.Content)
		totalLines := len(splitLines(doc.Content))
		start, end, found := webfetch.SectionRange(sections, sectionID, totalLines)
		if !found {
			return nil, mapWebError(webfetch.NewCodedError(fmt.Errorf("unknown section %q", sectionID), webfetch.ErrorCodeInvalidArgument, "use a section id from the toc view"))
		}
		text, err := json.Marshal(struct {
			Section string `json:"section"`
			Start   int    `json:"start_line"`
			End     int    `json:"end_line"`
			Content string `json:"content"`
		}{Section: sectionID, Start: start, End: end, Content: joinLines(splitLines(doc.Content)[start:end])})
		if err != nil {
			return nil, fmt.Errorf("encode section view: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "view": "section", "section": sectionID}}, nil
	case "must_contain":
		terms, err := stringSliceArg(args, "must_contain")
		if err != nil {
			return nil, mapWebError(err)
		}
		clean, err := webfetch.MustContainTerms(terms)
		if err != nil {
			return nil, mapWebError(err)
		}
		hits := make([]webfetch.ProbeHit, 0, len(clean))
		for _, term := range clean {
			hit, probeErr := webfetch.ProbeDocument(doc.Content, term)
			if probeErr != nil {
				return nil, mapWebError(probeErr)
			}
			hits = append(hits, hit)
		}
		text, err := json.Marshal(struct {
			Hits []webfetch.ProbeHit `json:"hits"`
		}{Hits: hits})
		if err != nil {
			return nil, fmt.Errorf("encode must_contain view: %w", err)
		}
		return &ToolResult{Text: string(text), Meta: map[string]any{"url": doc.URL, "final_url": doc.FinalURL, "view": "must_contain", "terms": len(clean)}}, nil
	default:
		return nil, mapWebError(webfetch.NewCodedError(fmt.Errorf("unsupported view %q", view), webfetch.ErrorCodeInvalidArgument, "use toc, section, or must_contain"))
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
