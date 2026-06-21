package jinn

import (
	"path"
	"strings"
)

// splitOnOperators splits cmdline on unquoted |, ;, &, newline, &&, ||.
// This is not a full bash parser — it handles the common cases.
func splitOnOperators(cmdline string) []string {
	var segments []string
	var cur strings.Builder
	var quote shellQuoteState

	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote.writeLiteral(ch, &cur) {
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

var directShellWrappers = map[string]bool{
	"{": true, "(": true, "!": true,
	"if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true,
	"nohup": true,
}

var flagSkippingShellWrappers = map[string]map[string]bool{
	"time": nil,
	"nice": {"-n": true, "--adjustment": true},
}

var shellWrapperPayloadIndex = map[string]func([]string) int{
	"coproc":  coprocPayloadIndex,
	"case":    casePayloadIndex,
	"exec":    execPayloadIndex,
	"command": commandPayloadIndex,
	"builtin": commandPayloadIndex,
	"busybox": multiplexerPayloadIndex,
	"toybox":  multiplexerPayloadIndex,
}

func restPayload(tokens []string) ([]string, bool) {
	if len(tokens) == 0 {
		return nil, false
	}
	return tokens, true
}

func indexedPayload(tokens []string, index int) ([]string, bool) {
	if index < 0 {
		return nil, false
	}
	return tokens[index:], true
}

func coprocPayloadIndex(tokens []string) int {
	if len(tokens) == 0 {
		return -1
	}
	if len(tokens) >= 2 && isShellName(tokens[0]) && (tokens[1] == "{" || tokens[1] == "(") {
		return 1
	}
	return 0
}

func casePayloadIndex(tokens []string) int {
	for i, tok := range tokens {
		if isCasePatternToken(tok) && i+1 < len(tokens) {
			return i + 1
		}
	}
	return -1
}

func isCasePatternToken(tok string) bool {
	return strings.HasSuffix(tok, ")") && tok != ")"
}

func wrapperPayloadIndex(tokens []string, flagArgs map[string]bool) int {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if isEnvAssignment(tok) {
			continue
		}
		if flagArgs != nil && flagArgs[tok] {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return i
	}
	return -1
}

func normalizedCommandVerb(tok string) string {
	tok = commandVerbBeforeRedirection(tok)
	if tok == "" {
		return ""
	}
	verb := path.Base(tok)
	verb = strings.TrimLeft(verb, "({")
	verb = strings.TrimRight(verb, ")}")
	if verb == "" {
		return path.Base(tok)
	}
	return verb
}

func commandVerbBeforeRedirection(tok string) string {
	for i, ch := range tok {
		if ch != '<' && ch != '>' {
			continue
		}
		if i == 0 || allDigits(tok[:i]) {
			return ""
		}
		return tok[:i]
	}
	return tok
}

func envPayload(tokens []string) ([]string, bool) {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if isEnvAssignment(tok) {
			continue
		}
		switch {
		case tok == "-u" || tok == "--unset":
			i++
			continue
		case tok == "-S" || tok == "--split-string":
			if i+1 >= len(tokens) {
				return nil, false
			}
			payload := shellFields(tokens[i+1])
			if len(payload) == 0 {
				return nil, false
			}
			return payload, true
		case strings.HasPrefix(tok, "--split-string="):
			payload := shellFields(strings.TrimPrefix(tok, "--split-string="))
			if len(payload) == 0 {
				return nil, false
			}
			return payload, true
		case strings.HasPrefix(tok, "-"):
			continue
		default:
			return tokens[i:], true
		}
	}
	return nil, false
}

func execPayloadIndex(tokens []string) int {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if isEnvAssignment(tok) {
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return i
	}
	return -1
}

func commandPayloadIndex(tokens []string) int {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if isEnvAssignment(tok) {
			continue
		}
		if tok == "-v" || tok == "-V" {
			return -1
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return i
	}
	return -1
}

func multiplexerPayloadIndex(tokens []string) int {
	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return i
	}
	return -1
}

// hasOutputRedirection reports whether seg contains an unquoted shell output
// or read/write redirection operator. Read-only verbs become state-changing
// once the shell opens a target for stdout/stderr or read/write access,
// including compact forms like "echo x>file", "2>err", and "cat <>file".
func hasOutputRedirection(seg string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	runes := []rune(seg)
	for i, ch := range runes {
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
		case !inSingle && !inDouble && isShellWriteRedirection(runes, i):
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
	tokens := shellFields(seg)
	for i, tok := range tokens {
		target, consumesNext, ok := outputRedirectionTarget(tok)
		if ok && target == "" && consumesNext && i+1 < len(tokens) {
			target = tokens[i+1]
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

func isDangerousDevicePath(path string) bool {
	switch {
	case path == "/dev/null" || path == "/dev/stdout" || path == "/dev/stderr":
		return false
	case strings.HasPrefix(path, "/dev/sd"):
		return true
	case strings.HasPrefix(path, "/dev/hd"):
		return true
	case strings.HasPrefix(path, "/dev/vd"):
		return true
	case strings.HasPrefix(path, "/dev/xvd"):
		return true
	case strings.HasPrefix(path, "/dev/nvme"):
		return true
	case strings.HasPrefix(path, "/dev/mmcblk"):
		return true
	case strings.HasPrefix(path, "/dev/disk"):
		return true
	case strings.HasPrefix(path, "/dev/rdisk"):
		return true
	case strings.HasPrefix(path, "/dev/mapper/"):
		return true
	default:
		return false
	}
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
		close := findMatchingBrace(runes, open)
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
		close := findMatchingBrace(runes, open)
		out.WriteString(string(runes[offset : open+1]))
		if close <= open {
			break
		}
		out.WriteRune('}')
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
		if runes[i] == '{' && looksLikeFunctionHeader(string(runes[:i])) {
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
