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

// Subcommand-level overrides for multi-purpose verbs.
// Known subcommands replace the parent verb's base risk.
// Conservative: only mark obvious read-only actions as safe.
var subcommandRiskRules = map[string]map[string]riskRule{
	"git": {
		"status": {RiskSafe, "git status — read-only project status"},
		"log":    {RiskSafe, "git log — read-only history"},
		"show":   {RiskSafe, "git show — read-only object inspection"},
		"diff":   {RiskSafe, "git diff — read-only diff output"},
		"push":   {RiskDangerous, "git push — modifies remote refs"},
	},
	"go": {
		"env":     {RiskSafe, "go env — read-only environment query"},
		"version": {RiskSafe, "go version — read-only"},
		"list":    {RiskSafe, "go list — read-only package query"},
		"doc":     {RiskSafe, "go doc — read-only docs"},
		"help":    {RiskSafe, "go help — read-only"},
	},
}

// Flags that consume the next token as an argument for specific verbs.
var subcommandFlagArgRules = map[string]map[string]bool{
	"git": {
		"-C":          true,
		"--git-dir":   true,
		"--work-tree": true,
		"--namespace": true,
	},
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
// Each per-verb helper returns (level, reason, true) when its heuristic fires.
func applyArgHeuristics(verb string, tokens []string, base riskRule) (RiskLevel, string) {
	if level, reason, fired := applySubcommandRule(verb, tokens); fired {
		subLevel := level
		subReason := reason

		// Keep destructive git heuristics from being masked by safe defaults.
		if level2, reason2, fired2 := checkGitForcePush(tokens); fired2 && level2 > subLevel {
			return level2, reason2
		}

		return subLevel, subReason
	}

	var (
		level  RiskLevel
		reason string
		fired  bool
	)
	switch verb {
	case "rm":
		level, reason, fired = checkRmFlags(tokens)
	case "chmod":
		level, reason, fired = checkChmodArgs(tokens)
	case "git":
		level, reason, fired = checkGitForcePush(tokens)
	case "find":
		level, reason, fired = checkFindExecRm(tokens)
	}
	if fired {
		return level, reason
	}
	return base.Level, base.Reason
}

// applySubcommandRule looks up a more specific risk rule for verbs that support
// recognized subcommands (e.g. "git status" as a safe read-only command).
func applySubcommandRule(verb string, tokens []string) (RiskLevel, string, bool) {
	rules, ok := subcommandRiskRules[verb]
	if !ok {
		return 0, "", false
	}

	subcommand := firstSubcommandToken(verb, tokens)
	if subcommand == "" {
		return 0, "", false
	}

	rule, ok := rules[subcommand]
	if !ok {
		return 0, "", false
	}
	return rule.Level, rule.Reason, true
}

// firstSubcommandToken returns the first non-assignment token after the verb,
// skipping flags (and their arguments where known) that are command setup.
func firstSubcommandToken(verb string, tokens []string) string {
	if len(tokens) < 2 {
		return ""
	}

	consumeNextArg := subcommandFlagArgRules[verb]

	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			if i+1 < len(tokens) {
				return tokens[i+1]
			}
			return ""
		}
		if strings.HasPrefix(tok, "-") {
			if consumeNextArg != nil && consumeNextArg[tok] {
				i++
			}
			continue
		}
		if isEnvAssignment(tok) {
			continue
		}
		if eq := strings.IndexByte(tok, '='); eq > 0 && eq < len(tok)-1 {
			if strings.HasPrefix(tok[:eq], "-") {
				continue
			}
		}
		return tok
	}
	return ""
}

// checkRmFlags escalates rm with force/recursive flags to dangerous.
func checkRmFlags(tokens []string) (RiskLevel, string, bool) {
	for _, tok := range tokens[1:] {
		if isFlag(tok) && containsAny(tok, 'r', 'f') {
			return RiskDangerous, "critical: rm with force/recursive flags — irreversible", true
		}
	}
	return 0, "", false
}

// checkChmodArgs flags world-writable chmod modes.
func checkChmodArgs(tokens []string) (RiskLevel, string, bool) {
	for _, tok := range tokens[1:] {
		if tok == "777" || tok == "0777" || tok == "a+rwx" {
			return RiskCaution, "chmod 777 — grants world read/write/execute", true
		}
	}
	return 0, "", false
}

// checkGitForcePush flags git push with --force/-f.
func checkGitForcePush(tokens []string) (RiskLevel, string, bool) {
	for i, tok := range tokens[1:] {
		if tok == "push" {
			for _, f := range tokens[i+2:] {
				if f == "--force" || f == "-f" {
					return RiskCaution, "git force push — rewrites remote history", true
				}
			}
		}
	}
	return 0, "", false
}

// checkFindExecRm flags find -exec rm as bulk deletion.
func checkFindExecRm(tokens []string) (RiskLevel, string, bool) {
	for i, tok := range tokens {
		if tok == "-exec" && i+1 < len(tokens) && tokens[i+1] == "rm" {
			return RiskDangerous, "find -exec rm — bulk file deletion", true
		}
	}
	return 0, "", false
}
