package webfetch

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ProjectionFormatMarkdown selects the default content projection.
const ProjectionFormatMarkdown = "markdown"

var (
	headingPattern  = regexp.MustCompile(`^#{1,6}\s+`)
	linkPattern     = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)`)
	autoLinkPattern = regexp.MustCompile(`<((?:https?://|mailto:)[^>]+)>`)
)

// OutputLimits bounds projected content. Zero means unlimited.
type OutputLimits struct {
	MaxBytes int
	MaxLines int
}

// ContentProjection describes a bounded view of fetched content.
type ContentProjection struct {
	Content           string
	Format            string
	Truncated         bool
	TruncatedBy       string
	TotalBytes        int
	OutputBytes       int
	TotalLines        int
	OutputLines       int
	StartLine         int
	StartLineByte     int
	NextStartLine     int
	NextStartLineByte int
}

// NewOutputLimits validates optional byte and line limits.
func NewOutputLimits(maxBytes, maxLines int) (OutputLimits, error) {
	if maxBytes < 0 {
		return OutputLimits{}, newCodedError(errors.New("max-bytes must be non-negative"), ErrorCodeInvalidArgument, "use zero for unlimited output or provide a positive byte limit")
	}
	if maxLines < 0 {
		return OutputLimits{}, newCodedError(errors.New("max-lines must be non-negative"), ErrorCodeInvalidArgument, "use zero for unlimited output or provide a positive line limit")
	}
	return OutputLimits{MaxBytes: maxBytes, MaxLines: maxLines}, nil
}

// ProjectContent applies byte and line limits without changing content format.
func ProjectContent(content string, limits OutputLimits) ContentProjection {
	projection := ContentProjection{
		Content:    content,
		TotalBytes: len(content),
		TotalLines: ContentLineCount(content),
	}
	if (limits.MaxBytes <= 0 || projection.TotalBytes <= limits.MaxBytes) &&
		(limits.MaxLines <= 0 || projection.TotalLines <= limits.MaxLines) {
		projection.OutputBytes = projection.TotalBytes
		projection.OutputLines = projection.TotalLines
		return projection
	}

	projected := content
	if limits.MaxLines > 0 && projection.TotalLines > limits.MaxLines {
		projected = prefixByLines(projected, limits.MaxLines)
		projection.TruncatedBy = truncatedByLines
	}
	if limits.MaxBytes > 0 && len(projected) > limits.MaxBytes {
		projected = prefixByBytes(projected, limits.MaxBytes)
		projection.TruncatedBy = truncatedByBytes
	}
	if projected == content {
		projection.TruncatedBy = ""
	}
	projection.Content = projected
	projection.OutputBytes = len(projected)
	projection.OutputLines = ContentLineCount(projected)
	projection.Truncated = projected != content
	return projection
}

// ProjectFetchContent derives a requested representation, applies a zero-based
// line offset, and then enforces output limits.
func ProjectFetchContent(content, format string, startLine int, limits OutputLimits) (ContentProjection, error) {
	return ProjectFetchContentAt(content, format, startLine, 0, limits)
}

// ProjectFetchContentAt resumes at a byte boundary within the selected line.
func ProjectFetchContentAt(content, format string, startLine, startLineByte int, limits OutputLimits) (ContentProjection, error) {
	format = NormalizeProjectionFormat(format)
	if format == "" {
		format = ProjectionFormatMarkdown
	}
	if startLine < 0 || startLineByte < 0 {
		return ContentProjection{}, newCodedError(errors.New("start_line must be non-negative"), ErrorCodeInvalidArgument, "use a zero-based line offset")
	}
	derived, err := deriveProjectionContent(content, format)
	if err != nil {
		return ContentProjection{}, err
	}
	totalLines := ContentLineCount(derived)
	remaining := contentFromLine(derived, startLine)
	lineLength := len(remaining)
	if end := strings.IndexByte(remaining, '\n'); end >= 0 {
		lineLength = end
	}
	if startLineByte > lineLength || (startLineByte < len(remaining) && !utf8.RuneStart(remaining[startLineByte])) {
		return ContentProjection{}, newCodedError(errors.New("invalid start_line_byte"), ErrorCodeInvalidArgument, "use a UTF-8 byte boundary within start_line")
	}
	remaining = remaining[startLineByte:]
	projection := ProjectContent(remaining, limits)
	if remaining != "" && projection.Content == "" {
		return ContentProjection{}, newCodedError(errors.New("max_bytes cannot fit the next character"), ErrorCodeInvalidArgument, "increase max_bytes to fit the next UTF-8 character")
	}
	projection.Format = format
	projection.StartLine = startLine
	projection.StartLineByte = startLineByte
	projection.TotalBytes = len(derived)
	projection.TotalLines = totalLines
	if (startLine > 0 || startLineByte > 0) && !projection.Truncated {
		projection.Truncated = true
		projection.TruncatedBy = "offset"
	}
	if len(projection.Content) < len(remaining) {
		projection.NextStartLine = startLine + strings.Count(projection.Content, "\n")
		projection.NextStartLineByte = startLineByte + len(projection.Content)
		if last := strings.LastIndexByte(projection.Content, '\n'); last >= 0 {
			projection.NextStartLineByte = len(projection.Content) - last - 1
		}
	}
	return projection, nil
}

// NormalizeProjectionFormat trims and lowercases a projection name.
func NormalizeProjectionFormat(format string) string {
	return strings.ToLower(strings.TrimSpace(format))
}

// ProjectionFormatIsMarkdown reports whether format selects the default view.
func ProjectionFormatIsMarkdown(format string) bool {
	normalized := NormalizeProjectionFormat(format)
	return normalized == "" || normalized == ProjectionFormatMarkdown
}

// ContentLineCount counts logical lines without treating a trailing newline as
// an additional empty line.
func ContentLineCount(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		return len(lines) - 1
	}
	return len(lines)
}

// TruncationMarker renders the stable human-readable output budget marker.
func TruncationMarker(projection ContentProjection) string {
	return fmt.Sprintf("[truncated: showing %d/%d lines, %d/%d bytes (%s)]",
		projection.OutputLines,
		projection.TotalLines,
		projection.OutputBytes,
		projection.TotalBytes,
		projection.TruncatedBy,
	)
}

func deriveProjectionContent(content, format string) (string, error) {
	switch format {
	case ProjectionFormatMarkdown:
		return content, nil
	case "headings":
		return extractHeadings(content), nil
	case "links":
		return extractLinks(content), nil
	default:
		return "", newCodedError(errors.New("format must be markdown, headings, or links"), ErrorCodeInvalidArgument, "choose format markdown, headings, or links")
	}
}

func extractHeadings(content string) string {
	var headings strings.Builder
	for content != "" {
		line, rest, _ := strings.Cut(content, "\n")
		content = rest
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if headingPattern.MatchString(trimmed) {
			headings.WriteString(trimmed)
			headings.WriteByte('\n')
		}
	}
	return headings.String()
}

func extractLinks(content string) string {
	seen := make(map[string]struct{})
	var links strings.Builder
	appendLink := func(link string) {
		link = strings.TrimSpace(link)
		if link == "" {
			return
		}
		if _, ok := seen[link]; ok {
			return
		}
		seen[link] = struct{}{}
		links.WriteString(link)
		links.WriteByte('\n')
	}
	for _, match := range linkPattern.FindAllStringSubmatch(content, -1) {
		appendLink(match[1])
	}
	for _, match := range autoLinkPattern.FindAllStringSubmatch(content, -1) {
		appendLink(match[1])
	}
	return links.String()
}

func contentFromLine(content string, startLine int) string {
	if startLine <= 0 || content == "" {
		return content
	}
	for range startLine {
		_, rest, found := strings.Cut(content, "\n")
		if !found {
			return ""
		}
		content = rest
	}
	return content
}

func prefixByLines(content string, maxLines int) string {
	if maxLines <= 0 || ContentLineCount(content) <= maxLines {
		return content
	}
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "")
}

func prefixByBytes(content string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	prefix := utf8Prefix(content, maxBytes)
	if newline := strings.LastIndexByte(prefix, '\n'); newline >= 0 {
		return prefix[:newline+1]
	}
	return prefix
}

func utf8Prefix(content string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(content) <= maxBytes {
		return content
	}
	prefix := content[:maxBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}
