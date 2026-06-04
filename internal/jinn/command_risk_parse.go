package jinn

import "strings"

// splitOnOperators splits cmdline on unquoted |, ;, &&, ||.
// This is not a full bash parser — it handles the common cases.
func splitOnOperators(cmdline string) []string {
	var segments []string
	var cur strings.Builder
	inSingle := false
	inDouble := false

	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			cur.WriteRune(ch)
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			cur.WriteRune(ch)
		case inSingle || inDouble:
			cur.WriteRune(ch)
		case i+1 < len(runes) && (string(runes[i:i+2]) == "&&" || string(runes[i:i+2]) == "||"):
			segments = append(segments, cur.String())
			cur.Reset()
			i++ // skip second char of digraph
		case ch == '|' || ch == ';':
			segments = append(segments, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(ch)
		}
	}
	if cur.Len() > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

// containsSubshell reports whether cmdline has $(...) or backtick substitution.
func containsSubshell(cmdline string) bool {
	return strings.Contains(cmdline, "$(") || strings.ContainsRune(cmdline, '`')
}

// extractSubshellContent returns the body of the first $(...) or backtick pair.
func extractSubshellContent(cmdline string) string {
	if start := strings.Index(cmdline, "$("); start >= 0 {
		depth := 0
		for i := start; i < len(cmdline); i++ {
			switch cmdline[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return cmdline[start+2 : i]
				}
			}
		}
		return cmdline[start+2:]
	}
	first := strings.IndexRune(cmdline, '`')
	if first < 0 {
		return ""
	}
	second := strings.IndexRune(cmdline[first+1:], '`')
	if second < 0 {
		return cmdline[first+1:]
	}
	return cmdline[first+1 : first+1+second]
}

// findMatchingParen returns the index of the ) that matches the ( reached
// while scanning from start, tracking nesting. start points at the '$' of a
// "$(" opener; depth increments on each '(' and decrements on each ')', so the
// match is the ) that brings depth back to 0. Returns start (the initial scan
// position) when no matching ) is found — mirroring the unmatched fallthrough
// of the original inline loop.
func findMatchingParen(cmdline string, start int) int {
	depth := 0
	for i := start; i < len(cmdline); i++ {
		switch cmdline[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return start
}

// stripSubshells removes $(...) and backtick expressions so the outer verb
// is visible to classifySegment.
func stripSubshells(cmdline string) string {
	for strings.Contains(cmdline, "$(") {
		start := strings.Index(cmdline, "$(")
		end := findMatchingParen(cmdline, start)
		cmdline = cmdline[:start] + cmdline[end+1:]
	}
	for {
		first := strings.IndexRune(cmdline, '`')
		if first < 0 {
			break
		}
		second := strings.IndexRune(cmdline[first+1:], '`')
		if second < 0 {
			cmdline = cmdline[:first]
			break
		}
		cmdline = cmdline[:first] + cmdline[first+1+second+1:]
	}
	return cmdline
}

// classifyHeredoc detects heredoc syntax (<<EOF ... EOF) and classifies the
// embedded body for dangerous verbs. Returns (level, reason, true) when found.
// Minimum is RiskCaution — heredocs are always opaque at some level.
func classifyHeredoc(cmdline string) (RiskLevel, string, bool) {
	if !strings.Contains(cmdline, "<<") {
		return 0, "", false
	}

	lines := strings.Split(cmdline, "\n")
	maxLevel := RiskCaution
	reasons := []string{"heredoc"}
	inBody := false
	marker := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Header line: detect the heredoc marker and classify the outer verb.
		if !inBody {
			idx := strings.Index(trimmed, "<<")
			if idx < 0 {
				continue
			}
			marker = heredocMarker(trimmed[idx+2:])
			inBody = true
			classifyHeredocOuter(trimmed[:idx], &maxLevel, &reasons)
			continue
		}

		// Body line: terminator ends the body; blank lines are ignored.
		if trimmed == marker {
			inBody = false
			continue
		}
		if trimmed == "" {
			continue
		}
		lvl, reason := classifySegment(trimmed)
		if lvl > maxLevel {
			maxLevel = lvl
		}
		if lvl >= RiskDangerous {
			reasons = append(reasons, reason)
		}
	}

	return maxLevel, strings.Join(reasons, "; "), true
}

// heredocMarker extracts the terminator marker from the text after "<<",
// stripping the optional "-" (<<-EOF) and surrounding quotes.
func heredocMarker(afterOp string) string {
	raw := strings.TrimSpace(afterOp)
	raw = strings.TrimPrefix(raw, "-") // <<-EOF
	raw = strings.Trim(raw, `'"`)
	return strings.TrimSpace(raw)
}

// classifyHeredocOuter classifies the verb preceding "<<" and escalates
// maxLevel/reasons when it outranks the current level.
func classifyHeredocOuter(before string, maxLevel *RiskLevel, reasons *[]string) {
	outer := strings.TrimSpace(before)
	if outer == "" {
		return
	}
	rule, ok := riskTable[firstVerb(outer)]
	if !ok || rule.Level <= *maxLevel {
		return
	}
	*maxLevel = rule.Level
	*reasons = append(*reasons, rule.Reason)
}

// firstVerb returns the first non-assignment token from cmd.
func firstVerb(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if !isEnvAssignment(tok) {
			return tok
		}
	}
	return ""
}

// isEnvAssignment reports whether tok looks like VAR=val.
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for _, ch := range tok[:eq] {
		if !isIdentChar(ch) {
			return false
		}
	}
	return true
}

func isIdentChar(ch rune) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') || ch == '_'
}

// isFlag reports whether tok starts with '-'.
func isFlag(tok string) bool { return strings.HasPrefix(tok, "-") }

// containsAny reports whether s (after stripping leading dashes) contains any
// of the given rune flags.
func containsAny(s string, flags ...rune) bool {
	s = strings.TrimLeft(s, "-")
	for _, f := range flags {
		if strings.ContainsRune(s, f) {
			return true
		}
	}
	return false
}
