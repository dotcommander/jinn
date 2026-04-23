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

// stripSubshells removes $(...) and backtick expressions so the outer verb
// is visible to classifySegment.
func stripSubshells(cmdline string) string {
	for strings.Contains(cmdline, "$(") {
		start := strings.Index(cmdline, "$(")
		depth, end := 0, start
		for i := start; i < len(cmdline); i++ {
			switch cmdline[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
					goto doneParens
				}
			}
		}
	doneParens:
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
		if !inBody {
			idx := strings.Index(trimmed, "<<")
			if idx < 0 {
				continue
			}
			raw := strings.TrimSpace(trimmed[idx+2:])
			raw = strings.TrimPrefix(raw, "-") // <<-EOF
			raw = strings.Trim(raw, `'"`)
			marker = strings.TrimSpace(raw)
			inBody = true
			// Classify the outer verb (before <<).
			if outer := strings.TrimSpace(trimmed[:idx]); outer != "" {
				if rule, ok := riskTable[firstVerb(outer)]; ok && rule.Level > maxLevel {
					maxLevel = rule.Level
					reasons = append(reasons, rule.Reason)
				}
			}
			continue
		}
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
