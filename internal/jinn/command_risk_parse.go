package jinn

import (
	"strings"
)

// splitOnOperators splits cmdline on unquoted |, ;, &, newline, &&, ||.
// This is not a full bash parser — it handles the common cases.
func splitOnOperators(cmdline string) []string {
	var segments []string
	var cur strings.Builder
	var quote shellQuoteState
	spans := outputComparisonSkipSpans(cmdline)
	spanIdx := 0

	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote.writeLiteral(ch, &cur) {
			continue
		}
		if runeInOutputSpan(i, spans, &spanIdx) {
			cur.WriteRune(ch)
			continue
		}
		if i+1 < len(runes) && isShellDoubleOperator(runes[i], runes[i+1]) {
			segments = append(segments, cur.String())
			cur.Reset()
			i++ // skip second char of digraph
			continue
		}
		if isShellCommandSeparator(ch) {
			segments = append(segments, cur.String())
			cur.Reset()
			continue
		}
		if isShellBackgroundSeparator(runes, i) {
			segments = append(segments, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(ch)
	}
	if cur.Len() > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

type shellQuoteState struct {
	inSingle bool
	inDouble bool
	escaped  bool
}

func (s *shellQuoteState) writeLiteral(ch rune, b *strings.Builder) bool {
	switch {
	case s.escaped:
		s.escaped = false
	case ch == '\\' && !s.inSingle:
		s.escaped = true
	case ch == '\'' && !s.inDouble:
		s.inSingle = !s.inSingle
	case ch == '"' && !s.inSingle:
		s.inDouble = !s.inDouble
	case s.inSingle || s.inDouble:
	default:
		return false
	}
	if b != nil {
		b.WriteRune(ch)
	}
	return true
}

func isShellDoubleOperator(left, right rune) bool {
	return (left == '&' && right == '&') || (left == '|' && right == '|')
}

func isShellCommandSeparator(ch rune) bool {
	return ch == '|' || ch == ';' || ch == '\n'
}

func isShellBackgroundSeparator(runes []rune, i int) bool {
	if runes[i] != '&' {
		return false
	}
	if i > 0 && (runes[i-1] == '>' || runes[i-1] == '<') {
		return false
	}
	return true
}

func stripShellComments(cmdline string) string {
	var out strings.Builder
	var quote shellQuoteState
	atWordStart := true

	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote.writeLiteral(ch, &out) {
			atWordStart = false
			continue
		}
		if ch == '#' && atWordStart {
			for i+1 < len(runes) && runes[i+1] != '\n' {
				i++
			}
			continue
		}
		out.WriteRune(ch)
		atWordStart = isCommentWordStart(ch)
	}
	return out.String()
}

func isCommentWordStart(ch rune) bool {
	return ch == '\n' || ch == ';' || ch == '|' || ch == '&' || ch == '(' || ch == '{' || isShellBlank(ch)
}

func isShellBlank(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\r'
}

func shellFields(s string) []string {
	var fields []string
	var cur strings.Builder
	var quote shellQuoteState
	inField := false

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote.escaped {
			quote.escaped = false
			cur.WriteRune(ch)
			inField = true
			continue
		}
		if ch == '\\' && !quote.inSingle {
			quote.escaped = true
			inField = true
			continue
		}
		if ch == '\'' && !quote.inDouble {
			quote.inSingle = !quote.inSingle
			inField = true
			continue
		}
		if ch == '"' && !quote.inSingle {
			quote.inDouble = !quote.inDouble
			inField = true
			continue
		}
		if !quote.inSingle && !quote.inDouble && isShellBlank(ch) {
			if inField {
				fields = append(fields, cur.String())
				cur.Reset()
				inField = false
			}
			continue
		}
		cur.WriteRune(ch)
		inField = true
	}
	if inField {
		fields = append(fields, cur.String())
	}
	return fields
}

// effectiveCommand returns the executable that shell risk policy should judge,
// unwrapping common shell forms that otherwise hide the real verb. It preserves
// the returned token slice from that verb onward so per-command flag heuristics
// still see the command's own arguments.
func effectiveCommand(tokens []string) (string, []string) {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if isEnvAssignment(tok) {
			continue
		}
		if isCasePatternToken(tok) && i+1 < len(tokens) {
			return effectiveCommand(tokens[i+1:])
		}
		verb := normalizedCommandVerb(tok)
		if verb == "" {
			continue
		}
		if next, ok := wrappedCommandPayload(verb, tokens[i+1:]); ok {
			return effectiveCommand(next)
		}
		return verb, tokens[i:]
	}
	return "", nil
}

func wrappedCommandPayload(verb string, rest []string) ([]string, bool) {
	if directShellWrappers[verb] {
		return restPayload(rest)
	}
	if flagArgs, ok := flagSkippingShellWrappers[verb]; ok {
		return indexedPayload(rest, wrapperPayloadIndex(rest, flagArgs))
	}
	if verb == "env" {
		return envPayload(rest)
	}
	if payloadIndex, ok := shellWrapperPayloadIndex[verb]; ok {
		return indexedPayload(rest, payloadIndex(rest))
	}
	return nil, false
}
