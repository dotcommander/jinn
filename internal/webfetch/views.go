package webfetch

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Bounds for the web_fetch view/probe modes.
const (
	maxMustContainTerms  = 8
	maxMustContainChars  = 500
	excerptPad           = 80
	maxChromeQualityLen  = 700
	chromeProseThreshold = 0.5
)

// SectionInfo is one heading in the document outline.
type SectionInfo struct {
	ID    string
	Level int
	Title string
	Line  int
}

// ProbeHit is one must_contain result: counts plus excerpt evidence.
type ProbeHit struct {
	Term     string   `json:"term"`
	Found    bool     `json:"found"`
	Count    int      `json:"count"`
	Sections []string `json:"sections,omitempty"`
	Excerpts []string `json:"excerpts,omitempty"`
}

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

// DocumentOutline assigns stable section ids (s1, s2, ...) to markdown
// headings in document order, shared by toc/section/probe views.
func DocumentOutline(content string) []SectionInfo {
	var sections []SectionInfo
	for lineNo, line := range strings.Split(content, "\n") {
		if match := headingRE.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			sections = append(sections, SectionInfo{
				ID:    fmt.Sprintf("s%d", len(sections)+1),
				Level: len(match[1]),
				Title: strings.TrimSpace(match[2]),
				Line:  lineNo,
			})
		}
	}
	return sections
}

// RenderTOC renders the heading outline.
func RenderTOC(sections []SectionInfo) string {
	var builder strings.Builder
	for _, section := range sections {
		fmt.Fprintf(&builder, "%s L%d %s\n", section.ID, section.Line+1, section.Title)
	}
	return strings.TrimRight(builder.String(), "\n")
}

// SectionRange returns the line range [start, end) for a numbered section.
func SectionRange(sections []SectionInfo, sectionID string, totalLines int) (start, end int, found bool) {
	for i, section := range sections {
		if strings.EqualFold(section.ID, sectionID) {
			start := section.Line
			end := totalLines
			if i+1 < len(sections) {
				end = sections[i+1].Line
			}
			return start, end, true
		}
	}
	return 0, 0, false
}

// MustContainTerms validates and normalizes probe terms: at most
// maxMustContainTerms phrases/regexes, each at most maxMustContainChars.
func MustContainTerms(terms []string) ([]string, error) {
	var clean []string
	for _, term := range terms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			return nil, newCodedError(errors.New("must_contain: a term is empty"), ErrorCodeInvalidArgument, "provide non-empty probe terms")
		}
		if len([]rune(trimmed)) > maxMustContainChars {
			return nil, newCodedError(fmt.Errorf("must_contain: a term is longer than %d chars", maxMustContainChars), ErrorCodeInvalidArgument, "shorten the probe term")
		}
		clean = append(clean, trimmed)
	}
	if len(clean) > maxMustContainTerms {
		return nil, newCodedError(fmt.Errorf("must_contain: at most %d terms per call", maxMustContainTerms), ErrorCodeInvalidArgument, "split the probes across calls")
	}
	return clean, nil
}

// ProbeDocument answers one must_contain term: counts, matching section ids,
// and excerpts. Phrase terms match case-insensitively across whitespace;
// /re/ terms compile as RE2 regexes.
func ProbeDocument(content string, term string) (ProbeHit, error) {
	hit := ProbeHit{Term: term}
	var matcher *regexp.Regexp
	var err error
	if isRegexTerm(term) {
		source := strings.TrimPrefix(strings.TrimSuffix(term, "/"), "/")
		matcher, err = regexp.Compile("(?i)" + source)
		if err != nil {
			return ProbeHit{}, newCodedError(fmt.Errorf("must_contain: invalid regex %q: %w", term, err), ErrorCodeInvalidArgument, "provide a valid RE2 regex")
		}
	} else {
		matcher = regexp.MustCompile("(?i)" + regexp.QuoteMeta(term))
	}
	sections := DocumentOutline(content)
	seen := map[string]bool{}
	lines := strings.Split(content, "\n")
	for lineNo, line := range lines {
		matches := matcher.FindAllStringIndex(line, -1)
		if len(matches) == 0 {
			continue
		}
		hit.Count += len(matches)
		if section := sectionForLine(sections, lineNo); section != "" && !seen[section] {
			seen[section] = true
			hit.Sections = append(hit.Sections, section)
		}
		if len(hit.Excerpts) < 3 {
			hit.Excerpts = append(hit.Excerpts, excerptAround(line, matches[0]))
		}
	}
	hit.Found = hit.Count > 0
	return hit, nil
}

func isRegexTerm(term string) bool {
	return strings.HasPrefix(term, "/") && strings.HasSuffix(term, "/") && len(term) > 2
}

func sectionForLine(sections []SectionInfo, line int) string {
	last := ""
	for _, section := range sections {
		if section.Line > line {
			break
		}
		last = section.ID
	}
	return last
}

func excerptAround(line string, match []int) string {
	start := match[0] - excerptPad
	if start < 0 {
		start = 0
	}
	end := match[1] + excerptPad
	if end > len(line) {
		end = len(line)
	}
	return "..." + strings.TrimSpace(line[start:end]) + "..."
}

// ChromeOnly reports whether extracted text looks like navigation/login
// chrome rather than page content: small, auth-prompt-laden, prose-poor.
func ChromeOnly(markdown string) bool {
	chars := len([]rune(markdown))
	if chars == 0 || chars > maxChromeQualityLen {
		return false
	}
	lower := strings.ToLower(markdown)
	for _, prompt := range []string{
		"log in", "sign in", "sign up", "continue with email", "continue with phone",
		"continue with google", "continue with apple", "we use cookies", "cookie policy",
	} {
		if strings.Contains(lower, prompt) {
			return proseShare(markdown) < chromeProseThreshold
		}
	}
	return false
}

func proseShare(markdown string) float64 {
	total := len([]rune(markdown))
	if total == 0 {
		return 0
	}
	prose := 0
	for _, line := range strings.Split(markdown, "\n") {
		if len([]rune(line)) >= 60 && len(strings.Fields(line)) >= 8 {
			prose += len([]rune(line))
		}
	}
	return float64(prose) / float64(total)
}

// QualityOf returns the extraction quality signal for a document body.
func QualityOf(markdown string) string {
	if ChromeOnly(markdown) {
		return "chrome_only"
	}
	return "content"
}
