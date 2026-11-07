package jinn

import (
	"fmt"
	"strings"
)

// RiskLevel describes how dangerous a shell command is to execute.
type RiskLevel int

const (
	// RiskSafe — read-only, no side effects visible outside stdout.
	RiskSafe RiskLevel = iota
	// RiskCaution — modifies state, but generally recoverable.
	RiskCaution
	// RiskDangerous — destructive or irreversible.
	RiskDangerous
)

// String returns the lowercase label used in user-visible output.
func (r RiskLevel) String() string {
	switch r {
	case RiskSafe:
		return "safe"
	case RiskCaution:
		return "caution"
	case RiskDangerous:
		return "dangerous"
	default:
		return "unknown"
	}
}

type riskRule struct {
	Level  RiskLevel
	Reason string
}

// riskTable is the single source of truth for per-verb classification.
// Unknown verbs default to RiskCaution (conservative).
type argHeuristic func([]string) (RiskLevel, string, bool)

// ClassifyCommand parses cmdline (a bash-style command string) and returns
// the highest risk level it can detect plus a human reason.
//
// Conservative: unknown verbs default to RiskCaution, not RiskSafe.
// Pipelines (cmd1 | cmd2) return the MAX risk of any component.
// Heredocs and subshells are treated as opaque — RiskCaution minimum unless
// the content contains dangerous verbs.
func ClassifyCommand(cmdline string) (RiskLevel, string) {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return RiskCaution, "empty command"
	}

	// Heredocs: scan body for verbs before operator-splitting corrupts the text.
	if level, reason, ok := classifyHeredoc(cmdline); ok {
		return level, reason
	}
	cmdline = stripShellComments(cmdline)
	if strings.TrimSpace(cmdline) == "" {
		return RiskCaution, "empty command"
	}
	if bodies := extractFunctionBodyContents(cmdline); len(bodies) > 0 {
		maxLevel, reasons := classifyEmbeddedShellBodies("function body", cmdline, bodies)
		return maxLevel, strings.Join(reasons, " | ")
	}

	// Command and process substitutions execute nested commands; classify all
	// bodies before trusting the outer command's verb.
	if containsCommandExpansion(cmdline) {
		maxLevel, reasons := classifyCommandExpansion(cmdline)
		return maxLevel, strings.Join(reasons, " | ")
	}

	// Standard pipeline / conjunction: split, classify each, return max.
	maxLevel, reasons := classifyShellSegments(cmdline)
	if len(reasons) == 0 {
		return RiskCaution, "empty command"
	}
	if len(reasons) == 1 {
		return maxLevel, reasons[0]
	}
	return maxLevel, "pipeline: " + strings.Join(reasons, " | ")
}

func classifyShellSegments(cmdline string) (RiskLevel, []string) {
	segments := splitOnOperators(cmdline)
	maxLevel := RiskSafe
	reasons := make([]string, 0, len(segments))
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		lvl, reason := classifySegment(seg)
		if lvl > maxLevel {
			maxLevel = lvl
		}
		reasons = append(reasons, reason)
	}
	return maxLevel, reasons
}

func classifyCommandExpansion(cmdline string) (RiskLevel, []string) {
	outerLevel, reasons := classifyShellSegments(stripSubshells(cmdline))
	maxLevel := outerLevel

	for _, body := range extractSubshellContents(cmdline) {
		subLevel, subReason := ClassifyCommand(body)
		if subLevel > maxLevel {
			maxLevel = subLevel
		}
		reasons = append(reasons, "subshell: "+subReason)
	}
	for _, body := range extractProcessSubstitutionContents(cmdline) {
		subLevel, subReason := ClassifyCommand(body)
		if subLevel > maxLevel {
			maxLevel = subLevel
		}
		reasons = append(reasons, "process substitution: "+subReason)
	}
	return maxLevel, reasons
}

func classifyEmbeddedShellBodies(label, cmdline string, bodies []string) (RiskLevel, []string) {
	outerLevel, reasons := classifyOuterShellAfterFunctionStrip(stripFunctionBodies(cmdline))
	maxLevel := outerLevel

	for _, body := range bodies {
		bodyLevel, bodyReason := ClassifyCommand(body)
		if bodyLevel > maxLevel {
			maxLevel = bodyLevel
		}
		reasons = append(reasons, label+": "+bodyReason)
	}
	return maxLevel, reasons
}

func classifyOuterShellAfterFunctionStrip(cmdline string) (RiskLevel, []string) {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return RiskSafe, nil
	}
	if containsCommandExpansion(cmdline) {
		return classifyCommandExpansion(cmdline)
	}
	return classifyShellSegments(cmdline)
}

// ExplainRisk returns a one-line formatted explanation for the user,
// e.g. "dangerous: rm with force flags — irreversible".
func ExplainRisk(r RiskLevel, reason string) string {
	return fmt.Sprintf("%s: %s", r.String(), reason)
}

// classifySegment classifies a single operator-free command segment.
// Applies argument heuristics on top of the riskTable lookup.
func classifySegment(seg string) (RiskLevel, string) {
	tokens := shellFields(seg)
	if len(tokens) == 0 {
		return RiskCaution, "empty segment"
	}
	if onlyShellTerminators(tokens) {
		return RiskSafe, "shell terminator"
	}
	wrapperLevel, wrapperReason, wrapperWrites := timeOutputRisk(tokens)
	if wrapperLevel == RiskDangerous {
		return wrapperLevel, wrapperReason
	}

	verb, commandTokens := effectiveCommand(tokens)
	if verb == "" {
		return RiskCaution, "only environment assignments — no command verb"
	}

	if reason, dangerous := dangerousCommandInvocation(seg, verb, commandTokens); dangerous {
		return RiskDangerous, reason
	}

	rule, ok := riskTable[verb]
	if !ok {
		return RiskCaution, fmt.Sprintf("unknown command %q — defaulting to caution", verb)
	}
	level, reason := applyArgHeuristics(verb, commandTokens, rule)
	if target, ok := dangerousOutputRedirectionTarget(seg); ok {
		return RiskDangerous, "shell redirection to device " + target + " — can destroy data"
	}
	if hasOutputRedirection(seg) && level < RiskCaution {
		return RiskCaution, "shell output redirection — writes files"
	}
	if wrapperWrites && level < RiskCaution {
		return RiskCaution, wrapperReason
	}
	return level, reason
}

func dangerousCommandInvocation(seg, verb string, tokens []string) (string, bool) {
	switch {
	case isDynamicCommandVerb(verb):
		return "dynamic command expansion — arbitrary command execution", true
	case isFilesystemFormatter(verb):
		return "formats filesystem", true
	case isShellInterpreter(verb):
		return verb + " shell execution — arbitrary command execution", true
	case isInlineCodeExecution(verb, tokens):
		return verb + " inline code — arbitrary code execution", true
	}
	if reason, dangerous := dangerousStdinCodeInvocation(seg, verb, tokens); dangerous {
		return reason, true
	}
	return arbitraryCodeInvocation(verb, tokens)
}

func dangerousStdinCodeInvocation(seg, verb string, tokens []string) (string, bool) {
	if !isStdinCodeInterpreterInvocation(verb, tokens) {
		return "", false
	}
	if isImplicitNodeStdinInvocation(verb, tokens) {
		return "node input type without script — arbitrary code execution", true
	}
	if hasInputRedirection(seg) {
		return "input redirection feeds interpreter stdin — arbitrary code execution", true
	}
	program, _ := stdinCodeProgram(verb, tokens)
	if isStdinCodeProgram(program) {
		return verb + " explicit stdin program — arbitrary code execution", true
	}
	return "", false
}

// timeOutputRisk keeps /usr/bin/time's output-file side effect visible after
// the wrapper has been unwrapped to classify its payload.
func timeOutputRisk(tokens []string) (RiskLevel, string, bool) {
	for i, tok := range tokens {
		if isEnvAssignment(tok) {
			continue
		}
		verb := normalizedCommandVerb(tok)
		if verb == "" {
			continue
		}
		if verb == "time" {
			return classifyTimeInvocation(tokens[i+1:])
		}
		if payload, ok := wrappedCommandPayload(verb, tokens[i+1:]); ok {
			return timeOutputRisk(payload)
		}
		return RiskSafe, "", false
	}
	return RiskSafe, "", false
}

func classifyTimeInvocation(tokens []string) (RiskLevel, string, bool) {
	invocation := parseTimeInvocation(tokens)
	level, reason, found := RiskSafe, "", false
	for _, target := range invocation.outputTargets {
		found = true
		if isDangerousDevicePath(target) {
			level, reason = RiskDangerous, "time output to device "+target+" — can destroy data"
			break
		}
		level, reason = RiskCaution, "time output — writes file"
	}
	if invocation.payloadIndex < 0 {
		return level, reason, found
	}
	payloadLevel, payloadReason, payloadFound := timeOutputRisk(tokens[invocation.payloadIndex:])
	if payloadLevel > level {
		return payloadLevel, payloadReason, payloadFound || found
	}
	return level, reason, found || payloadFound
}

func isFilesystemFormatter(verb string) bool {
	return verb == "mkfs" || strings.HasPrefix(verb, "mkfs.")
}

// arbitraryCodeInvocation identifies command frontends whose ordinary purpose
// is to evaluate user-controlled programs, task files, or downloaded packages.
func arbitraryCodeInvocation(verb string, tokens []string) (string, bool) {
	switch verb {
	case "awk", "gawk", "make", "npx", "bunx":
		return verb + " execution — arbitrary code execution", true
	case "pnpm", "bun":
		args := packageManagerPayload(tokens)
		if len(args) == 0 {
			return "", false
		}
		if verb == "pnpm" && (args[0] == "dlx" || args[0] == "exec") {
			return verb + " " + args[0] + " — arbitrary code execution", true
		}
		if verb == "bun" && args[0] == "x" {
			return "bun x — arbitrary code execution", true
		}
	}
	return "", false
}

func onlyShellTerminators(tokens []string) bool {
	for _, tok := range tokens {
		if !isShellTerminator(tok) {
			return false
		}
	}
	return true
}

func isShellTerminator(tok string) bool {
	return tok == "fi" || tok == "done" || tok == "esac"
}

func isDynamicCommandVerb(verb string) bool {
	return strings.Contains(verb, "$")
}
