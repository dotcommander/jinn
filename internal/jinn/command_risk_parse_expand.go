package jinn

import (
	"strings"
)

// containsCommandExpansion reports whether cmdline has command or process
// substitution syntax that executes nested shell code.
func containsCommandExpansion(cmdline string) bool {
	return len(extractSubshellContents(cmdline)) > 0 || containsProcessSubstitution(cmdline)
}

// extractSubshellContents returns every $(...) and backtick body in cmdline.
// ClassifyCommand uses this to avoid treating a safe first substitution as proof
// that later substitutions are also safe.
func extractSubshellContents(cmdline string) []string {
	var contents []string
	var quote commandExpansionQuoteState
	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote.consume(ch) {
			continue
		}
		if ch == '$' && i+1 < len(runes) && runes[i+1] == '(' {
			if i+2 < len(runes) && runes[i+2] == '(' {
				continue
			}
			end := findMatchingParenRunes(runes, i)
			if end <= i {
				contents = append(contents, string(runes[i+2:]))
				break
			}
			contents = append(contents, string(runes[i+2:end]))
			i = end
			continue
		}
		if ch == '`' {
			end := findNextBacktick(runes, i+1)
			if end < 0 {
				contents = append(contents, string(runes[i+1:]))
				break
			}
			contents = append(contents, string(runes[i+1:end]))
			i = end
		}
	}
	return contents
}

type commandExpansionQuoteState struct {
	inSingle bool
	inDouble bool
	escaped  bool
}

func (s *commandExpansionQuoteState) consume(ch rune) bool {
	switch {
	case s.escaped:
		s.escaped = false
		return true
	case ch == '\\' && !s.inSingle:
		s.escaped = true
		return true
	case ch == '\'' && !s.inDouble:
		s.inSingle = !s.inSingle
		return true
	case ch == '"' && !s.inSingle:
		s.inDouble = !s.inDouble
		return true
	case s.inSingle:
		return true
	default:
		return false
	}
}

func findMatchingParenRunes(runes []rune, start int) int {
	depth := 0
	var quote shellQuoteState
	for i := start; i < len(runes); i++ {
		ch := runes[i]
		if quote.writeLiteral(ch, nil) {
			continue
		}
		switch ch {
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

func findNextBacktick(runes []rune, start int) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == '`' {
			return i
		}
	}
	return -1
}

func containsProcessSubstitution(cmdline string) bool {
	return len(extractProcessSubstitutionContents(cmdline)) > 0
}

func extractProcessSubstitutionContents(cmdline string) []string {
	var contents []string
	var quote shellQuoteState
	runes := []rune(cmdline)
	for i := 0; i+1 < len(runes); i++ {
		ch := runes[i]
		if quote.writeLiteral(ch, nil) {
			continue
		}
		if (ch == '<' || ch == '>') && runes[i+1] == '(' {
			end := findMatchingParenRunes(runes, i+1)
			if end <= i+1 {
				contents = append(contents, string(runes[i+2:]))
				break
			}
			contents = append(contents, string(runes[i+2:end]))
			i = end
		}
	}
	return contents
}

func extractFunctionBodyContents(cmdline string) []string {
	var contents []string
	runes := []rune(cmdline)
	for offset := 0; offset < len(runes); {
		open, ok := nextFunctionBodyOpen(runes, offset)
		if !ok {
			break
		}
		close := findMatchingFunctionBody(runes, open)
		if close <= open {
			contents = append(contents, string(runes[open+1:]))
			break
		}
		contents = append(contents, string(runes[open+1:close]))
		offset = close + 1
	}
	return contents
}

func stripFunctionBodies(cmdline string) string {
	var out strings.Builder
	runes := []rune(cmdline)
	for offset := 0; offset < len(runes); {
		open, ok := nextFunctionBodyOpen(runes, offset)
		if !ok {
			out.WriteString(string(runes[offset:]))
			break
		}
		close := findMatchingFunctionBody(runes, open)
		out.WriteString(string(runes[offset : open+1]))
		if close <= open {
			break
		}
		out.WriteRune(runes[close])
		offset = close + 1
	}
	return out.String()
}

func nextFunctionBodyOpen(runes []rune, offset int) (int, bool) {
	var quote shellQuoteState
	for i := offset; i < len(runes); i++ {
		if quote.writeLiteral(runes[i], nil) {
			continue
		}
		if (runes[i] == '{' || runes[i] == '(') && looksLikeFunctionHeader(string(runes[:i])) {
			return i, true
		}
	}
	return -1, false
}

func looksLikeFunctionHeader(prefix string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	last := fields[len(fields)-1]
	if strings.HasSuffix(last, "()") {
		return true
	}
	if len(fields) >= 2 && fields[len(fields)-2] == "function" && isShellName(last) {
		return true
	}
	return false
}

func findMatchingFunctionBody(runes []rune, open int) int {
	if runes[open] == '(' {
		return findMatchingParenRunes(runes, open)
	}
	return findMatchingBrace(runes, open)
}

func isShellName(s string) bool {
	if s == "" {
		return false
	}
	for i, ch := range s {
		if i == 0 {
			if !isShellNameStart(ch) {
				return false
			}
			continue
		}
		if !isIdentChar(ch) {
			return false
		}
	}
	return true
}

func isShellNameStart(ch rune) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_'
}

func findMatchingBrace(runes []rune, open int) int {
	depth := 0
	var quote shellQuoteState
	for i, ch := range runes {
		if i < open {
			continue
		}
		if quote.writeLiteral(ch, nil) {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return open
}

// stripSubshells removes $(...) and backtick expressions so the outer verb
// is visible to classifySegment.
func stripSubshells(cmdline string) string {
	var out strings.Builder
	var quote commandExpansionQuoteState
	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote.consume(ch) {
			out.WriteRune(ch)
			continue
		}
		if ch == '$' && i+1 < len(runes) && runes[i+1] == '(' {
			end := findMatchingParenRunes(runes, i)
			if end <= i {
				break
			}
			i = end
			continue
		}
		if ch == '`' {
			end := findNextBacktick(runes, i+1)
			if end < 0 {
				break
			}
			i = end
			continue
		}
		out.WriteRune(ch)
	}
	return out.String()
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
				classifyHeredocShellLine(trimmed, &maxLevel, &reasons)
				continue
			}
			var suffix string
			marker, suffix = heredocHeaderParts(trimmed[idx+2:])
			inBody = true
			classifyHeredocHeaderLine(trimmed[:idx], &maxLevel, &reasons)
			classifyHeredocShellLine(suffix, &maxLevel, &reasons)
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
		lvl, reason := ClassifyCommand(trimmed)
		if lvl > maxLevel {
			maxLevel = lvl
		}
		if lvl >= RiskDangerous {
			reasons = append(reasons, reason)
		}
	}

	return maxLevel, strings.Join(reasons, "; "), true
}

func heredocHeaderParts(afterOp string) (marker string, suffix string) {
	raw := strings.TrimSpace(afterOp)
	raw = strings.TrimPrefix(raw, "-") // <<-EOF
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if raw[0] == '\'' || raw[0] == '"' {
		quote := raw[0]
		for i := 1; i < len(raw); i++ {
			if raw[i] == quote {
				return raw[1:i], raw[i+1:]
			}
		}
		return strings.Trim(raw, `'"`), ""
	}
	for i, ch := range raw {
		if isShellBlank(ch) || isShellCommandSeparator(ch) || ch == '&' {
			return raw[:i], raw[i:]
		}
	}
	return raw, ""
}

func classifyHeredocHeaderLine(line string, maxLevel *RiskLevel, reasons *[]string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if isShellHeredocReader(line) {
		return
	}
	if isStdinCodeHeredocReader(line) {
		if RiskDangerous > *maxLevel {
			*maxLevel = RiskDangerous
		}
		*reasons = append(*reasons, "heredoc feeds interpreter stdin — arbitrary code execution")
		return
	}
	classifyHeredocShellLine(line, maxLevel, reasons)
}

func isShellHeredocReader(line string) bool {
	tokens := shellFields(line)
	verb, commandTokens := effectiveCommand(tokens)
	if !isShellInterpreter(verb) {
		return false
	}
	for _, tok := range commandTokens[1:] {
		if tok == "-c" || tok == "--command" || !isFlag(tok) {
			return false
		}
	}
	return true
}

func isStdinCodeHeredocReader(line string) bool {
	tokens := shellFields(line)
	verb, commandTokens := effectiveCommand(tokens)
	if !isInlineCodeInterpreter(verb) {
		return false
	}
	for _, tok := range commandTokens[1:] {
		if tok == "-" {
			continue
		}
		if !isFlag(tok) {
			return false
		}
	}
	return true
}

func classifyHeredocShellLine(line string, maxLevel *RiskLevel, reasons *[]string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	lvl, reason := ClassifyCommand(line)
	if lvl > *maxLevel {
		*maxLevel = lvl
	}
	if lvl >= RiskDangerous {
		*reasons = append(*reasons, reason)
	}
}

// firstVerb returns the first non-assignment token from cmd.
func firstVerb(cmd string) string {
	for _, tok := range shellFields(cmd) {
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
