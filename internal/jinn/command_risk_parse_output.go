package jinn

import (
	"path"
	"sort"
	"strings"
)

// hasOutputRedirection reports whether seg contains an unquoted shell output
// or read/write redirection operator. Read-only verbs become state-changing
// once the shell opens a target for stdout/stderr or read/write access,
// including compact forms like "echo x>file", "2>err", and "cat <>file".
func hasOutputRedirection(seg string) bool {
	spans := outputComparisonSkipSpans(seg)
	var quote shellQuoteState
	spanIdx := 0

	runes := []rune(seg)
	for i, ch := range runes {
		if quote.writeLiteral(ch, nil) {
			continue
		}
		if runeInOutputSpan(i, spans, &spanIdx) {
			continue
		}
		if isShellWriteRedirection(runes, i) {
			return true
		}
	}
	return false
}

func runeInOutputSpan(i int, spans []outputSpan, spanIdx *int) bool {
	for *spanIdx < len(spans) && i >= spans[*spanIdx].end {
		(*spanIdx)++
	}
	return *spanIdx < len(spans) && i >= spans[*spanIdx].start
}

func outputComparisonSkipSpans(seg string) []outputSpan {
	var spans []outputSpan
	tokens := tokenizeOutputComparisonTokens(seg)
	spans = append(spans, outputSquareComparisonSpans(tokens)...)
	spans = append(spans, outputArithmeticComparisonSpans([]rune(seg))...)
	if len(spans) <= 1 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].start < spans[j].start
	})
	merged := spans[:0]
	for _, span := range spans {
		if len(merged) == 0 || span.start > merged[len(merged)-1].end {
			merged = append(merged, span)
			continue
		}
		if span.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = span.end
		}
	}
	return merged
}

type outputSpan struct {
	start int
	end   int
}

type outputComparisonToken struct {
	text    string
	start   int
	end     int
	quoted  bool
	escaped bool
}

func tokenizeOutputComparisonTokens(seg string) []outputComparisonToken {
	runes := []rune(seg)
	scanner := outputComparisonTokenizer{tokenStart: -1}
	for i, ch := range runes {
		scanner.consume(i, ch)
	}
	scanner.flush(len(runes))
	return scanner.tokens
}

type outputComparisonTokenizer struct {
	tokens       []outputComparisonToken
	cur          strings.Builder
	quote        shellQuoteState
	tokenStart   int
	tokenQuoted  bool
	tokenEscaped bool
}

func (s *outputComparisonTokenizer) consume(i int, ch rune) {
	if s.consumeEscape(i, ch) || s.consumeQuote(i, ch) {
		return
	}
	if !s.quote.inSingle && !s.quote.inDouble && isShellBlank(ch) {
		s.flush(i)
		return
	}
	s.start(i)
	s.cur.WriteRune(ch)
}

func (s *outputComparisonTokenizer) consumeEscape(i int, ch rune) bool {
	if s.quote.escaped {
		s.start(i)
		s.cur.WriteRune(ch)
		s.tokenEscaped = true
		s.quote.escaped = false
		return true
	}
	if ch != '\\' || s.quote.inSingle {
		return false
	}
	s.start(i)
	s.tokenEscaped = true
	s.quote.escaped = true
	return true
}

func (s *outputComparisonTokenizer) consumeQuote(i int, ch rune) bool {
	switch {
	case ch == '\'' && !s.quote.inDouble:
		s.quote.inSingle = !s.quote.inSingle
	case ch == '"' && !s.quote.inSingle:
		s.quote.inDouble = !s.quote.inDouble
	default:
		return false
	}
	s.start(i)
	s.tokenQuoted = true
	return true
}

func (s *outputComparisonTokenizer) start(i int) {
	if s.tokenStart < 0 {
		s.tokenStart = i
	}
}

func (s *outputComparisonTokenizer) flush(end int) {
	if s.tokenStart < 0 {
		return
	}
	s.tokens = append(s.tokens, outputComparisonToken{
		text:    s.cur.String(),
		start:   s.tokenStart,
		end:     end,
		quoted:  s.tokenQuoted,
		escaped: s.tokenEscaped,
	})
	s.cur.Reset()
	s.tokenStart = -1
	s.tokenQuoted = false
	s.tokenEscaped = false
}

func isCommandPositionStartToken(tok string) bool {
	switch tok {
	case "!", "if", "while", "until", "elif", "then", "do", "&&", "||", ";", "|":
		return true
	default:
		return false
	}
}

func outputSquareComparisonSpans(tokens []outputComparisonToken) []outputSpan {
	var spans []outputSpan
	openStart := -1
	commandPos := true

	for _, tok := range tokens {
		if openStart >= 0 {
			if !tok.quoted && !tok.escaped && tok.text == "]]" {
				spans = append(spans, outputSpan{start: openStart, end: tok.end})
				openStart = -1
			}
		} else if commandPos && !tok.quoted && !tok.escaped && tok.text == "[[" {
			openStart = tok.start
		}
		if isCommandPositionStartToken(tok.text) && !tok.quoted && !tok.escaped {
			commandPos = true
			continue
		}
		commandPos = false
	}
	return spans
}

func outputArithmeticComparisonSpans(runes []rune) []outputSpan {
	scanner := outputArithmeticScanner{openStart: -1}
	for i := 0; i < len(runes); i++ {
		if scanner.quote.writeLiteral(runes[i], nil) {
			continue
		}
		i += scanner.consume(runes, i)
	}
	return scanner.spans
}

type outputArithmeticScanner struct {
	spans      []outputSpan
	quote      shellQuoteState
	openStart  int
	parenDepth int
}

func (s *outputArithmeticScanner) consume(runes []rune, i int) int {
	ch := runes[i]
	doubleParen := i+1 < len(runes) && runes[i+1] == ch
	if s.openStart < 0 {
		if ch == '(' && doubleParen {
			s.openStart = i
			s.parenDepth = 0
			return 1
		}
		return 0
	}
	if ch == '(' {
		s.parenDepth++
		if doubleParen {
			return 1
		}
		return 0
	}
	if ch != ')' {
		return 0
	}
	if doubleParen {
		if s.parenDepth == 0 {
			s.spans = append(s.spans, outputSpan{start: s.openStart, end: i + 2})
			s.openStart = -1
		} else {
			s.parenDepth--
		}
		return 1
	}
	if s.parenDepth > 0 {
		s.parenDepth--
	}
	return 0
}

func tokenInOutputSpan(start, end int, spans []outputSpan) bool {
	for _, span := range spans {
		if end <= span.start {
			return false
		}
		if start < span.end && end > span.start {
			return true
		}
	}
	return false
}

func hasInputRedirection(seg string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	for _, ch := range seg {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case !inSingle && !inDouble && ch == '<':
			return true
		}
	}
	return false
}

func isShellWriteRedirection(runes []rune, i int) bool {
	ch := runes[i]
	if ch == '>' {
		return true
	}
	return ch == '<' && i+1 < len(runes) && runes[i+1] == '>'
}

func dangerousOutputRedirectionTarget(seg string) (string, bool) {
	tokens := tokenizeOutputComparisonTokens(seg)
	spans := outputComparisonSkipSpans(seg)
	for i, tok := range tokens {
		if tokenInOutputSpan(tok.start, tok.end, spans) {
			continue
		}
		target, consumesNext, ok := outputRedirectionTarget(tok.text)
		if ok && target == "" && consumesNext && i+1 < len(tokens) {
			target = tokens[i+1].text
		}
		if ok && isDangerousDevicePath(target) {
			return target, true
		}
	}
	return "", false
}

func outputRedirectionTarget(tok string) (target string, consumesNext bool, ok bool) {
	switch tok {
	case ">", ">|", ">>", "&>", "&>>", "<>":
		return "", true, true
	}
	if target, ok := compactOutputRedirectionTarget(tok, "<>"); ok {
		return target, target == "", true
	}
	if target, ok := compactOutputRedirectionTarget(tok, ">"); ok {
		return target, target == "", true
	}
	return "", false, false
}

func compactOutputRedirectionTarget(tok, op string) (string, bool) {
	index := strings.Index(tok, op)
	if index < 0 {
		return "", false
	}
	prefix := tok[:index]
	if prefix != "" && !allDigits(prefix) {
		return tok[index+len(op):], true
	}
	return tok[index+len(op):], true
}

var dangerousDevicePrefixes = [...]string{
	"/dev/sd", "/dev/hd", "/dev/vd", "/dev/xvd", "/dev/nvme", "/dev/mmcblk",
	"/dev/disk", "/dev/rdisk", "/dev/loop", "/dev/md", "/dev/dm-", "/dev/nbd",
	"/dev/rbd", "/dev/drbd", "/dev/pmem", "/dev/bcache", "/dev/dasd", "/dev/aoe/",
	"/dev/cciss/", "/dev/ram", "/dev/mtd", "/dev/zram", "/dev/ada", "/dev/da",
	"/dev/nda", "/dev/wd", "/dev/vtbd", "/dev/ld", "/dev/cgd", "/dev/vnd",
	"/dev/dsk/", "/dev/rdsk/", "/dev/zvol/", "/dev/mapper/",
}

func isDangerousDevicePath(target string) bool {
	if strings.HasPrefix(target, "/") {
		target = path.Clean(target)
	}
	if target == "/dev/null" || target == "/dev/stdout" || target == "/dev/stderr" {
		return false
	}
	for _, prefix := range dangerousDevicePrefixes {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

func isInputRedirectionToken(tok string) bool {
	if strings.HasPrefix(tok, "<") {
		return true
	}
	for i, ch := range tok {
		if ch == '<' {
			return i > 0 && allDigits(tok[:i])
		}
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return false
}

func inputRedirectionConsumesNext(tok string) bool {
	if tok == "<" {
		return true
	}
	for i, ch := range tok {
		if ch == '<' {
			return i == len(tok)-1
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
