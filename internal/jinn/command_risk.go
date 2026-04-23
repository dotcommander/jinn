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
var riskTable = map[string]riskRule{
	// SAFE — read-only, no side effects visible outside stdout.
	"ls":       {RiskSafe, "lists files"},
	"cat":      {RiskSafe, "reads file contents"},
	"grep":     {RiskSafe, "searches text"},
	"rg":       {RiskSafe, "searches text"},
	"find":     {RiskSafe, "locates files"},
	"head":     {RiskSafe, "reads file prefix"},
	"tail":     {RiskSafe, "reads file suffix"},
	"wc":       {RiskSafe, "counts lines/words"},
	"stat":     {RiskSafe, "reads file metadata"},
	"file":     {RiskSafe, "identifies file type"},
	"pwd":      {RiskSafe, "prints working directory"},
	"echo":     {RiskSafe, "prints arguments"},
	"printf":   {RiskSafe, "prints arguments"},
	"date":     {RiskSafe, "prints time"},
	"env":      {RiskSafe, "lists environment"},
	"which":    {RiskSafe, "locates executable"},
	"whoami":   {RiskSafe, "prints user"},
	"hostname": {RiskSafe, "prints hostname"},

	// Conservative defaults for multi-purpose tools.
	"go":  {RiskCaution, "go toolchain — may build or modify files"},
	"git": {RiskCaution, "git can modify state; some subcommands are safe but whole-verb is caution"},

	// CAUTION — modifies files, but recoverable.
	"cp":    {RiskCaution, "copies files"},
	"mv":    {RiskCaution, "moves/renames files"},
	"mkdir": {RiskCaution, "creates directory"},
	"touch": {RiskCaution, "creates/updates file"},
	"chmod": {RiskCaution, "changes permissions"},
	"chown": {RiskCaution, "changes ownership"},
	"sed":   {RiskCaution, "text edit; -i modifies files"},
	"awk":   {RiskCaution, "text processing"},
	"tar":   {RiskCaution, "archives; may extract over files"},
	"zip":   {RiskCaution, "creates archive"},
	"unzip": {RiskCaution, "extracts archive"},
	"curl":  {RiskCaution, "network request; may write files with -o"},
	"wget":  {RiskCaution, "downloads files"},

	// DANGEROUS — destructive, hard to reverse.
	"rm":        {RiskDangerous, "removes files — irreversible"},
	"rmdir":     {RiskDangerous, "removes directory"},
	"dd":        {RiskDangerous, "raw disk write — can destroy data"},
	"mkfs":      {RiskDangerous, "formats filesystem"},
	"mkfs.ext4": {RiskDangerous, "formats filesystem"},
	"mkfs.xfs":  {RiskDangerous, "formats filesystem"},
	"fdisk":     {RiskDangerous, "partitions disk"},
	"parted":    {RiskDangerous, "partitions disk"},
	"shred":     {RiskDangerous, "overwrites and deletes"},
	"wipe":      {RiskDangerous, "secure delete"},
	"fsck":      {RiskDangerous, "repairs filesystem — can corrupt if misused"},
	"reboot":    {RiskDangerous, "reboots system"},
	"shutdown":  {RiskDangerous, "halts system"},
	"halt":      {RiskDangerous, "halts system"},
	"poweroff":  {RiskDangerous, "powers off"},
	"kill":      {RiskDangerous, "signals process"},
	"killall":   {RiskDangerous, "signals multiple processes"},
	"sudo":      {RiskDangerous, "elevated privileges — treat entire command as dangerous"},
	"su":        {RiskDangerous, "switches user — elevated privileges"},
}

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

	// Subshells: classify inner content, then outer command; return max.
	if containsSubshell(cmdline) {
		subLevel, subReason := classifySegment(extractSubshellContent(cmdline))
		outerLevel, outerReason := classifySegment(stripSubshells(cmdline))
		if subLevel > outerLevel {
			return subLevel, "subshell: " + subReason
		}
		return outerLevel, outerReason
	}

	// Standard pipeline / conjunction: split, classify each, return max.
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
	if len(reasons) == 0 {
		return RiskCaution, "empty command"
	}
	if len(reasons) == 1 {
		return maxLevel, reasons[0]
	}
	return maxLevel, "pipeline: " + strings.Join(reasons, " | ")
}

// ExplainRisk returns a one-line formatted explanation for the user,
// e.g. "dangerous: rm with force flags — irreversible".
func ExplainRisk(r RiskLevel, reason string) string {
	return fmt.Sprintf("%s: %s", r.String(), reason)
}

// classifySegment classifies a single operator-free command segment.
// Applies argument heuristics on top of the riskTable lookup.
func classifySegment(seg string) (RiskLevel, string) {
	tokens := strings.Fields(seg)
	if len(tokens) == 0 {
		return RiskCaution, "empty segment"
	}

	// Skip leading VAR=val environment assignments to find the actual verb.
	verb := ""
	for _, tok := range tokens {
		if !isEnvAssignment(tok) {
			verb = tok
			break
		}
	}
	if verb == "" {
		return RiskCaution, "only environment assignments — no command verb"
	}

	// Shell verbs appearing as the target of a pipe are dangerous (curl ... | sh).
	if verb == "sh" || verb == "bash" || verb == "zsh" {
		return RiskDangerous, "pipe to shell — arbitrary code execution"
	}

	rule, ok := riskTable[verb]
	if !ok {
		return RiskCaution, fmt.Sprintf("unknown command %q — defaulting to caution", verb)
	}
	return applyArgHeuristics(verb, tokens, rule)
}

// applyArgHeuristics upgrades risk based on flag/argument patterns.
// It returns the base rule unchanged when no heuristic fires.
func applyArgHeuristics(verb string, tokens []string, base riskRule) (RiskLevel, string) {
	switch verb {
	case "rm":
		for _, tok := range tokens[1:] {
			if isFlag(tok) && containsAny(tok, 'r', 'f') {
				return RiskDangerous, "critical: rm with force/recursive flags — irreversible"
			}
		}
	case "chmod":
		for _, tok := range tokens[1:] {
			if tok == "777" || tok == "0777" || tok == "a+rwx" {
				return RiskCaution, "chmod 777 — grants world read/write/execute"
			}
		}
	case "git":
		for i, tok := range tokens[1:] {
			if tok == "push" {
				for _, f := range tokens[i+2:] {
					if f == "--force" || f == "-f" {
						return RiskCaution, "git force push — rewrites remote history"
					}
				}
			}
		}
	case "find":
		for i, tok := range tokens {
			if tok == "-exec" && i+1 < len(tokens) && tokens[i+1] == "rm" {
				return RiskDangerous, "find -exec rm — bulk file deletion"
			}
		}
	}
	return base.Level, base.Reason
}
