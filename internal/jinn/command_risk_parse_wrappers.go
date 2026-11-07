package jinn

import (
	"path"
	"strings"
)

var directShellWrappers = map[string]bool{
	"{": true, "(": true, "!": true,
	"if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true,
	"nohup": true,
}

var flagSkippingShellWrappers = map[string]map[string]bool{
	"nice":   {"-n": true, "--adjustment": true},
	"setsid": nil,
	"stdbuf": {"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true},
}

var shellWrapperPayloadIndex = map[string]func([]string) int{
	"coproc":  coprocPayloadIndex,
	"case":    casePayloadIndex,
	"exec":    execPayloadIndex,
	"command": commandPayloadIndex,
	"builtin": commandPayloadIndex,
	"busybox": multiplexerPayloadIndex,
	"toybox":  multiplexerPayloadIndex,
	"timeout": timeoutPayloadIndex,
	"taskset": tasksetPayloadIndex,
	"time":    timePayloadIndex,
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
	if tok == "[[" || strings.HasPrefix(tok, "((") {
		return "true"
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
		if payload, split := envSplitStringPayload(tokens, i); split {
			return payload, len(payload) > 0
		}
		if envOptionConsumesArgument(tok) {
			i++
			continue
		}
		if isAttachedEnvOption(tok) {
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tokens[i:], true
	}
	return nil, false
}

func envSplitStringPayload(tokens []string, index int) ([]string, bool) {
	token := tokens[index]
	if token == "-S" || token == "--split-string" {
		if index+1 >= len(tokens) {
			return nil, true
		}
		return shellFields(tokens[index+1]), true
	}
	if value, ok := strings.CutPrefix(token, "--split-string="); ok {
		return shellFields(value), true
	}
	return nil, false
}

func envOptionConsumesArgument(token string) bool {
	switch token {
	case "-u", "--unset", "-C", "--chdir", "-a", "--argv0", "-P":
		return true
	default:
		return false
	}
}

func isAttachedEnvOption(token string) bool {
	if strings.HasPrefix(token, "--chdir=") || strings.HasPrefix(token, "--argv0=") {
		return true
	}
	return len(token) > 2 && (strings.HasPrefix(token, "-C") || strings.HasPrefix(token, "-a") || strings.HasPrefix(token, "-P"))
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

// timeout accepts a duration between its options and the command payload.
// Skipping only flags would otherwise classify the duration as the executable,
// allowing a destructive command to hide behind the wrapper.
func timeoutPayloadIndex(tokens []string) int {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			if i+2 < len(tokens) {
				return i + 2 // duration, then command
			}
			return -1
		}
		if timeoutFlagConsumesArg(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if i+1 < len(tokens) {
			return i + 1 // duration, then command
		}
		return -1
	}
	return -1
}

func timeoutFlagConsumesArg(tok string) bool {
	switch tok {
	case "-k", "--kill-after", "-s", "--signal":
		return true
	default:
		return false
	}
}

type timeInvocation struct {
	outputTargets []string
	payloadIndex  int
}

func timePayloadIndex(tokens []string) int {
	return parseTimeInvocation(tokens).payloadIndex
}

// parseTimeInvocation stops at the wrapped command, so payload arguments such
// as "echo -o /dev/sda" cannot be mistaken for time output options.
func parseTimeInvocation(tokens []string) timeInvocation {
	invocation := timeInvocation{payloadIndex: -1}
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "--":
			invocation.payloadIndex = nextArgumentIndex(tokens, i)
			return invocation
		case isTimeFormatOption(tok):
			i++
		case isTimeOutputOption(tok):
			i = appendNextTimeOutputTarget(&invocation, tokens, i)
		case strings.HasPrefix(tok, "--format="):
			continue
		case strings.HasPrefix(tok, "--output="):
			invocation.outputTargets = append(invocation.outputTargets, strings.TrimPrefix(tok, "--output="))
		case strings.HasPrefix(tok, "-f") && len(tok) > len("-f"):
			continue
		case strings.HasPrefix(tok, "-o") && len(tok) > len("-o"):
			invocation.outputTargets = append(invocation.outputTargets, tok[len("-o"):])
		case strings.HasPrefix(tok, "-"):
			continue
		default:
			invocation.payloadIndex = i
			return invocation
		}
	}
	return invocation
}

func nextArgumentIndex(tokens []string, index int) int {
	if index+1 < len(tokens) {
		return index + 1
	}
	return -1
}

func isTimeFormatOption(token string) bool {
	return token == "-f" || token == "--format"
}

func isTimeOutputOption(token string) bool {
	return token == "-o" || token == "--output"
}

func appendNextTimeOutputTarget(invocation *timeInvocation, tokens []string, index int) int {
	next := nextArgumentIndex(tokens, index)
	if next < 0 {
		return index
	}
	invocation.outputTargets = append(invocation.outputTargets, tokens[next])
	return next
}

// taskset has one required affinity argument before the wrapped command. In
// CPU-list mode that affinity is consumed by -c/--cpu-list instead; -p edits or
// reads an existing PID and does not execute a payload command.
func tasksetPayloadIndex(tokens []string) int {
	cpuListMode := false
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			return tasksetPayloadAfterAffinity(tokens, i+1)
		}
		if isTasksetPIDOption(tok) {
			return -1
		}
		if tok == "-c" || tok == "--cpu-list" {
			if i+1 >= len(tokens) {
				return -1
			}
			cpuListMode = true
			i++
			continue
		}
		if isAttachedTasksetCPUListOption(tok) {
			cpuListMode = true
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tasksetPayloadFromArgument(tokens, i, cpuListMode)
	}
	return -1
}

func isTasksetPIDOption(token string) bool {
	return token == "-p" || token == "--pid"
}

func isAttachedTasksetCPUListOption(token string) bool {
	return strings.HasPrefix(token, "--cpu-list=") || (strings.HasPrefix(token, "-c") && len(token) > 2)
}

func tasksetPayloadFromArgument(tokens []string, index int, cpuListMode bool) int {
	if cpuListMode {
		return index
	}
	return tasksetPayloadAfterAffinity(tokens, index)
}

func tasksetPayloadAfterAffinity(tokens []string, affinityIndex int) int {
	if affinityIndex+1 < len(tokens) {
		return affinityIndex + 1
	}
	return -1
}
