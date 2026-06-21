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
	"command":  {RiskSafe, "shell command lookup"},
	"builtin":  {RiskSafe, "shell builtin lookup"},
	"true":     {RiskSafe, "shell true — no side effects"},
	"false":    {RiskSafe, "shell false — no side effects"},

	// Conservative defaults for multi-purpose tools.
	"go":  {RiskCaution, "go toolchain — may build or modify files"},
	"git": {RiskCaution, "git can modify state; some subcommands are safe but whole-verb is caution"},

	// CAUTION — modifies files, but recoverable.
	"cp":        {RiskCaution, "copies files"},
	"mv":        {RiskCaution, "moves/renames files"},
	"mkdir":     {RiskCaution, "creates directory"},
	"touch":     {RiskCaution, "creates/updates file"},
	"chmod":     {RiskCaution, "changes permissions"},
	"chown":     {RiskCaution, "changes ownership"},
	"sed":       {RiskCaution, "text edit; -i modifies files"},
	"awk":       {RiskCaution, "text processing"},
	"tar":       {RiskCaution, "archives; may extract over files"},
	"zip":       {RiskCaution, "creates archive"},
	"unzip":     {RiskCaution, "extracts archive"},
	"curl":      {RiskCaution, "network request; may write files with -o"},
	"wget":      {RiskCaution, "downloads files"},
	"rsync":     {RiskCaution, "syncs files; may overwrite destinations"},
	"tee":       {RiskCaution, "writes stdin to files"},
	"xargs":     {RiskCaution, "builds command lines from input"},
	"exec":      {RiskCaution, "replaces shell with command"},
	"alias":     {RiskCaution, "defines shell aliases"},
	"hash":      {RiskCaution, "updates shell command path cache"},
	"docker":    {RiskCaution, "container runtime — may modify local containers, images, and volumes"},
	"podman":    {RiskCaution, "container runtime — may modify local containers, images, and volumes"},
	"kubectl":   {RiskCaution, "Kubernetes CLI — may modify cluster resources"},
	"helm":      {RiskCaution, "Helm CLI — may modify cluster releases"},
	"terraform": {RiskCaution, "Terraform CLI — may modify infrastructure state"},
	"tofu":      {RiskCaution, "OpenTofu CLI — may modify infrastructure state"},
	"pulumi":    {RiskCaution, "Pulumi CLI — may modify infrastructure state"},
	"gh":        {RiskCaution, "GitHub CLI — may modify remote GitHub resources"},
	"brew":      {RiskCaution, "Homebrew CLI — may modify installed packages"},
	"apt":       {RiskCaution, "package manager — may modify installed packages"},
	"apt-get":   {RiskCaution, "package manager — may modify installed packages"},
	"dnf":       {RiskCaution, "package manager — may modify installed packages"},
	"yum":       {RiskCaution, "package manager — may modify installed packages"},
	"pacman":    {RiskCaution, "package manager — may modify installed packages"},
	"npm":       {RiskCaution, "package manager — may modify project dependencies"},
	"pnpm":      {RiskCaution, "package manager — may modify project dependencies"},
	"yarn":      {RiskCaution, "package manager — may modify project dependencies"},
	"bun":       {RiskCaution, "package manager — may modify project dependencies"},
	"pip":       {RiskCaution, "package manager — may modify Python packages"},
	"pip3":      {RiskCaution, "package manager — may modify Python packages"},
	"gem":       {RiskCaution, "package manager — may modify Ruby packages"},
	"cargo":     {RiskCaution, "package manager — may modify Rust packages"},
	"composer":  {RiskCaution, "package manager — may modify PHP dependencies"},
	"aws":       {RiskCaution, "AWS CLI — may modify cloud resources"},
	"az":        {RiskCaution, "Azure CLI — may modify cloud resources"},
	"gcloud":    {RiskCaution, "Google Cloud CLI — may modify cloud resources"},
	"psql":      {RiskCaution, "database CLI — may modify persistent data"},
	"mysql":     {RiskCaution, "database CLI — may modify persistent data"},
	"sqlite3":   {RiskCaution, "database CLI — may modify persistent data"},

	// DANGEROUS — destructive, hard to reverse.
	"rm":        {RiskDangerous, "removes files — irreversible"},
	"rmdir":     {RiskDangerous, "removes directory"},
	"unlink":    {RiskDangerous, "removes file — irreversible"},
	"dd":        {RiskDangerous, "raw disk write — can destroy data"},
	"mkfs":      {RiskDangerous, "formats filesystem"},
	"mkfs.ext4": {RiskDangerous, "formats filesystem"},
	"mkfs.xfs":  {RiskDangerous, "formats filesystem"},
	"fdisk":     {RiskDangerous, "partitions disk"},
	"parted":    {RiskDangerous, "partitions disk"},
	"shred":     {RiskDangerous, "overwrites and deletes"},
	"wipe":      {RiskDangerous, "secure delete"},
	"truncate":  {RiskDangerous, "truncates file contents — can destroy data"},
	"fsck":      {RiskDangerous, "repairs filesystem — can corrupt if misused"},
	"reboot":    {RiskDangerous, "reboots system"},
	"shutdown":  {RiskDangerous, "halts system"},
	"halt":      {RiskDangerous, "halts system"},
	"poweroff":  {RiskDangerous, "powers off"},
	"kill":      {RiskDangerous, "signals process"},
	"killall":   {RiskDangerous, "signals multiple processes"},
	"sudo":      {RiskDangerous, "elevated privileges — treat entire command as dangerous"},
	"su":        {RiskDangerous, "switches user — elevated privileges"},
	"eval":      {RiskDangerous, "evaluates shell code — arbitrary command execution"},
	"source":    {RiskDangerous, "sources shell code — arbitrary command execution"},
	".":         {RiskDangerous, "sources shell code — arbitrary command execution"},
	"trap":      {RiskDangerous, "registers shell code handler — arbitrary command execution"},
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

	verb, commandTokens := effectiveCommand(tokens)
	if verb == "" {
		return RiskCaution, "only environment assignments — no command verb"
	}

	if isDynamicCommandVerb(verb) {
		return RiskDangerous, "dynamic command expansion — arbitrary command execution"
	}
	// Shell interpreters execute arbitrary command text from flags, files, or stdin.
	if isShellInterpreter(verb) {
		return RiskDangerous, verb + " shell execution — arbitrary command execution"
	}
	if isInlineCodeExecution(verb, commandTokens) {
		return RiskDangerous, verb + " inline code — arbitrary code execution"
	}
	if hasInputRedirection(seg) && isStdinCodeInterpreterInvocation(verb, commandTokens) {
		return RiskDangerous, "input redirection feeds interpreter stdin — arbitrary code execution"
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
	return level, reason
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

func isInlineCodeExecution(verb string, tokens []string) bool {
	inlineFlags := inlineCodeFlags(verb)
	if len(inlineFlags) == 0 {
		return false
	}
	for _, tok := range tokens[1:] {
		for flag := range inlineFlags {
			if matchesInlineCodeFlag(tok, flag) {
				return true
			}
		}
	}
	return false
}

func matchesInlineCodeFlag(tok, flag string) bool {
	if tok == flag {
		return true
	}
	if flag != "" && strings.HasPrefix(tok, flag+"=") {
		return true
	}
	return isAttachedShortInlineFlag(tok, flag)
}

func isAttachedShortInlineFlag(tok, flag string) bool {
	if len(flag) != 2 || len(tok) <= len(flag) || tok[0] != '-' || strings.HasPrefix(tok, "--") {
		return false
	}
	return strings.ContainsRune(tok[1:], rune(flag[1]))
}

func inlineCodeFlags(verb string) map[string]bool {
	if flags, ok := inlineCodeFlagRules[verb]; ok {
		return flags
	}
	if isPythonVerb(verb) {
		return inlineCodeFlagRules["python"]
	}
	if strings.HasPrefix(verb, "lua5.") {
		return inlineCodeFlagRules["lua"]
	}
	return nil
}

var inlineCodeFlagRules = map[string]map[string]bool{
	"python":         {"-c": true},
	"node":           {"-e": true, "--eval": true, "-p": true, "--print": true},
	"nodejs":         {"-e": true, "--eval": true, "-p": true, "--print": true},
	"perl":           {"-e": true, "-E": true},
	"ruby":           {"-e": true},
	"php":            {"-r": true},
	"lua":            {"-e": true},
	"Rscript":        {"-e": true, "--expression": true},
	"rscript":        {"-e": true, "--expression": true},
	"julia":          {"-e": true, "--eval": true},
	"osascript":      {"-e": true},
	"pwsh":           {"-c": true, "-Command": true, "-command": true, "-EncodedCommand": true, "-encodedcommand": true},
	"powershell":     {"-c": true, "-Command": true, "-command": true, "-EncodedCommand": true, "-encodedcommand": true},
	"powershell.exe": {"-c": true, "-Command": true, "-command": true, "-EncodedCommand": true, "-encodedcommand": true},
	"cmd":            {"/c": true, "/C": true},
	"cmd.exe":        {"/c": true, "/C": true},
}

func isInlineCodeInterpreter(verb string) bool {
	return len(inlineCodeFlags(verb)) > 0
}

func isStdinCodeInterpreterInvocation(verb string, tokens []string) bool {
	if !isInlineCodeInterpreter(verb) {
		return false
	}
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		if isInputRedirectionToken(tok) {
			if inputRedirectionConsumesNext(tok) {
				i++
			}
			continue
		}
		if tok == "-" {
			continue
		}
		if !isFlag(tok) {
			return false
		}
	}
	return true
}

func isPythonVerb(verb string) bool {
	return verb == "python" || strings.HasPrefix(verb, "python2") || strings.HasPrefix(verb, "python3")
}

func isShellInterpreter(verb string) bool {
	switch verb {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "ash", "fish", "csh", "tcsh":
		return true
	default:
		return false
	}
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

	if heuristic, ok := argHeuristicForVerb(verb); ok {
		if level, reason, fired := heuristic(tokens); fired {
			return level, reason
		}
	}
	return base.Level, base.Reason
}

func argHeuristicForVerb(verb string) (argHeuristic, bool) {
	heuristics := map[string]argHeuristic{
		"rm": checkRmFlags,
		"cp": func(tokens []string) (RiskLevel, string, bool) {
			return checkFileCommandDeviceDestination("cp", tokens)
		},
		"mv": func(tokens []string) (RiskLevel, string, bool) {
			return checkFileCommandDeviceDestination("mv", tokens)
		},
		"chmod":     checkChmodArgs,
		"chown":     checkOwnershipDeviceTarget,
		"curl":      checkCurlOutputTarget,
		"wget":      checkWgetOutputTarget,
		"tar":       checkTarOutputTarget,
		"zip":       checkZipOutputTarget,
		"git":       checkGitDestructiveArgs,
		"find":      checkFindDelete,
		"xargs":     checkXargsRm,
		"rsync":     checkRsyncDelete,
		"alias":     checkAliasDefinitions,
		"hash":      checkHashBindings,
		"tee":       checkTeeTargets,
		"docker":    checkContainerRuntimeDestructiveArgs,
		"podman":    checkContainerRuntimeDestructiveArgs,
		"kubectl":   checkKubectlDestructiveArgs,
		"helm":      checkHelmDestructiveArgs,
		"terraform": checkTerraformDestructiveArgs,
		"tofu":      checkTerraformDestructiveArgs,
		"pulumi":    checkPulumiDestructiveArgs,
		"gh":        checkGitHubCLIDestructiveArgs,
		"brew":      checkPackageManagerDestructiveArgs,
		"apt":       checkPackageManagerDestructiveArgs,
		"apt-get":   checkPackageManagerDestructiveArgs,
		"dnf":       checkPackageManagerDestructiveArgs,
		"yum":       checkPackageManagerDestructiveArgs,
		"pacman":    checkPackageManagerDestructiveArgs,
		"npm":       checkPackageManagerDestructiveArgs,
		"pnpm":      checkPackageManagerDestructiveArgs,
		"yarn":      checkPackageManagerDestructiveArgs,
		"bun":       checkPackageManagerDestructiveArgs,
		"pip":       checkPackageManagerDestructiveArgs,
		"pip3":      checkPackageManagerDestructiveArgs,
		"gem":       checkPackageManagerDestructiveArgs,
		"cargo":     checkPackageManagerDestructiveArgs,
		"composer":  checkPackageManagerDestructiveArgs,
		"aws":       checkAWSDestructiveArgs,
		"az":        checkAzureDestructiveArgs,
		"gcloud":    checkGcloudDestructiveArgs,
		"psql":      checkSQLCommandArgs,
		"mysql":     checkSQLCommandArgs,
		"sqlite3":   checkSQLCommandArgs,
	}
	heuristic, ok := heuristics[verb]
	return heuristic, ok
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
		if isSubcommandSetupToken(tok) {
			if consumeNextArg != nil && consumeNextArg[tok] {
				i++
			}
			continue
		}
		return tok
	}
	return ""
}

// isSubcommandSetupToken reports whether tok is a flag, env assignment, or
// flag=value pair that precedes (and is not) the subcommand verb.
func isSubcommandSetupToken(tok string) bool {
	if strings.HasPrefix(tok, "-") {
		return true
	}
	if isEnvAssignment(tok) {
		return true
	}
	if eq := strings.IndexByte(tok, '='); eq > 0 && eq < len(tok)-1 {
		return strings.HasPrefix(tok[:eq], "-")
	}
	return false
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
	if target := lastNonFlagToken(tokens[1:]); isDangerousDevicePath(target) {
		return RiskDangerous, "chmod on device " + target + " — can compromise storage access", true
	}
	for _, tok := range tokens[1:] {
		if tok == "777" || tok == "0777" || tok == "a+rwx" {
			return RiskCaution, "chmod 777 — grants world read/write/execute", true
		}
	}
	return 0, "", false
}

func checkOwnershipDeviceTarget(tokens []string) (RiskLevel, string, bool) {
	if target := lastNonFlagToken(tokens[1:]); isDangerousDevicePath(target) {
		return RiskDangerous, "chown on device " + target + " — can compromise storage access", true
	}
	return 0, "", false
}

func checkFileCommandDeviceDestination(verb string, tokens []string) (RiskLevel, string, bool) {
	dest := lastNonFlagToken(tokens[1:])
	if isDangerousDevicePath(dest) {
		return RiskDangerous, verb + " to device " + dest + " — can destroy data", true
	}
	return 0, "", false
}

func lastNonFlagToken(tokens []string) string {
	for i := len(tokens) - 1; i >= 0; i-- {
		tok := tokens[i]
		if tok != "" && !strings.HasPrefix(tok, "-") {
			return tok
		}
	}
	return ""
}

func checkCurlOutputTarget(tokens []string) (RiskLevel, string, bool) {
	if target, ok := commandOptionTarget(tokens[1:], "-o", "--output"); ok && isDangerousDevicePath(target) {
		return RiskDangerous, "curl output to device " + target + " — can destroy data", true
	}
	return 0, "", false
}

func checkWgetOutputTarget(tokens []string) (RiskLevel, string, bool) {
	if target, ok := commandOptionTarget(tokens[1:], "-O", "--output-document"); ok && isDangerousDevicePath(target) {
		return RiskDangerous, "wget output to device " + target + " — can destroy data", true
	}
	return 0, "", false
}

func checkTarOutputTarget(tokens []string) (RiskLevel, string, bool) {
	target, ok := tarCreateOutputTarget(tokens[1:])
	if ok && isDangerousDevicePath(target) {
		return RiskDangerous, "tar output to device " + target + " — can destroy data", true
	}
	return 0, "", false
}

func tarCreateOutputTarget(tokens []string) (string, bool) {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "-f" || tok == "--file" {
			if i+1 < len(tokens) {
				return tokens[i+1], true
			}
			return "", true
		}
		if strings.HasPrefix(tok, "--file=") {
			return strings.TrimPrefix(tok, "--file="), true
		}
		if strings.HasPrefix(tok, "-") && strings.Contains(tok, "f") {
			if suffix := tok[strings.Index(tok, "f")+1:]; suffix != "" {
				return suffix, true
			}
			if i+1 < len(tokens) {
				return tokens[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

func checkZipOutputTarget(tokens []string) (RiskLevel, string, bool) {
	target := firstNonFlagToken(tokens[1:])
	if isDangerousDevicePath(target) {
		return RiskDangerous, "zip output to device " + target + " — can destroy data", true
	}
	return 0, "", false
}

func firstNonFlagToken(tokens []string) string {
	for _, tok := range tokens {
		if tok != "" && !strings.HasPrefix(tok, "-") {
			return tok
		}
	}
	return ""
}

func commandOptionTarget(tokens []string, shortFlag, longFlag string) (string, bool) {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == shortFlag || tok == longFlag:
			if i+1 < len(tokens) {
				return tokens[i+1], true
			}
			return "", true
		case strings.HasPrefix(tok, shortFlag) && len(tok) > len(shortFlag):
			return tok[len(shortFlag):], true
		case strings.HasPrefix(tok, longFlag+"="):
			return strings.TrimPrefix(tok, longFlag+"="), true
		}
	}
	return "", false
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

// checkGitDestructiveArgs flags git forms that discard local or remote state.
func checkGitDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	if level, reason, fired := checkGitForcePush(tokens); fired {
		return level, reason, true
	}
	if hasGitShellAlias(tokens) {
		return RiskDangerous, "git shell alias — arbitrary command execution", true
	}
	for i, tok := range tokens[1:] {
		switch tok {
		case "clean":
			if hasGitCleanDryRun(tokens[i+2:]) {
				return RiskSafe, "git clean dry-run — read-only deletion preview", true
			}
			return RiskDangerous, "git clean — deletes untracked files", true
		case "reset":
			for _, f := range tokens[i+2:] {
				if f == "--hard" {
					return RiskDangerous, "git reset --hard — discards local changes", true
				}
			}
		case "checkout", "restore":
			if hasPathspecDiscard(tokens[i+2:]) {
				return RiskDangerous, "git " + tok + " pathspec — discards local file changes", true
			}
		}
	}
	return 0, "", false
}

func hasGitShellAlias(tokens []string) bool {
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "-c" && i+1 < len(tokens) && isGitShellAliasConfig(tokens[i+1]) {
			return true
		}
		if strings.HasPrefix(tok, "-c") && len(tok) > len("-c") && isGitShellAliasConfig(tok[len("-c"):]) {
			return true
		}
		if strings.HasPrefix(tok, "--config=") && isGitShellAliasConfig(strings.TrimPrefix(tok, "--config=")) {
			return true
		}
	}
	return false
}

func isGitShellAliasConfig(config string) bool {
	key, value, ok := strings.Cut(config, "=")
	if !ok {
		return false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	return strings.HasPrefix(key, "alias.") && strings.HasPrefix(value, "!")
}

func hasGitCleanDryRun(tokens []string) bool {
	for _, tok := range tokens {
		if tok == "-n" || tok == "--dry-run" {
			return true
		}
		if strings.HasPrefix(tok, "-") && strings.Contains(tok, "n") {
			return true
		}
	}
	return false
}

func hasPathspecDiscard(tokens []string) bool {
	for _, tok := range tokens {
		if tok == "--" {
			return true
		}
	}
	return false
}

// checkFindDelete flags find forms that delete directly or delegate to a
// state-changing command through -exec/-execdir/-ok/-okdir.
func checkFindDelete(tokens []string) (RiskLevel, string, bool) {
	for i, tok := range tokens {
		if isFindDelegatingAction(tok) && i+1 < len(tokens) {
			if level, reason, ok := classifyFindExecPayload(tokens[i+1:]); ok {
				return level, reason, true
			}
		}
		if tok == "-delete" {
			return RiskDangerous, "find -delete — bulk file deletion", true
		}
		if isFindFileOutputAction(tok) {
			return RiskCaution, "find " + tok + " — writes output file", true
		}
	}
	return 0, "", false
}

func isFindDelegatingAction(tok string) bool {
	return tok == "-exec" || tok == "-execdir" || tok == "-ok" || tok == "-okdir"
}

func isFindFileOutputAction(tok string) bool {
	switch tok {
	case "-fprint", "-fprint0", "-fprintf", "-fls":
		return true
	default:
		return false
	}
}

func classifyFindExecPayload(tokens []string) (RiskLevel, string, bool) {
	var payload []string
	for _, tok := range tokens {
		if tok == ";" || tok == `\;` || tok == "+" {
			break
		}
		payload = append(payload, tok)
	}
	if len(payload) == 0 {
		return 0, "", false
	}
	level, reason := ClassifyCommand(strings.Join(payload, " "))
	if level <= RiskSafe {
		return 0, "", false
	}
	return level, "find -exec: " + reason, true
}

// checkXargsRm flags pipelines that hand a file list to a state-changing
// command through xargs.
func checkXargsRm(tokens []string) (RiskLevel, string, bool) {
	payload := xargsPayload(tokens[1:])
	if len(payload) == 0 {
		return 0, "", false
	}
	level, reason := ClassifyCommand(strings.Join(payload, " "))
	if level <= RiskSafe {
		return 0, "", false
	}
	return level, "xargs: " + reason, true
}

func xargsPayload(tokens []string) []string {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if xargsFlagConsumesArg(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "--max-") || strings.HasPrefix(tok, "--delimiter=") || strings.HasPrefix(tok, "-") {
			continue
		}
		return tokens[i:]
	}
	return nil
}

func xargsFlagConsumesArg(tok string) bool {
	switch tok {
	case "-0", "-r", "--null", "--no-run-if-empty":
		return false
	case "-a", "--arg-file", "-d", "--delimiter", "-E", "-I", "-i", "-L", "-l", "-n", "-P", "-s":
		return true
	default:
		return false
	}
}

func checkRsyncDelete(tokens []string) (RiskLevel, string, bool) {
	for _, tok := range tokens[1:] {
		if tok == "--delete" || strings.HasPrefix(tok, "--delete-") {
			return RiskDangerous, "rsync delete mode — deletes destination files", true
		}
		if tok == "--remove-source-files" {
			return RiskDangerous, "rsync remove-source-files — deletes source files after transfer", true
		}
	}
	return 0, "", false
}

func checkContainerRuntimeDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := containerRuntimePayload(tokens[1:])
	if len(args) == 0 {
		return 0, "", false
	}
	verb := tokens[0]
	if check, ok := containerRuntimeSubcommandChecks()[args[0]]; ok {
		return check(verb, args)
	}
	return 0, "", false
}

type containerRuntimeCheck func(string, []string) (RiskLevel, string, bool)

func containerRuntimeSubcommandChecks() map[string]containerRuntimeCheck {
	return map[string]containerRuntimeCheck{
		"system":    checkContainerRuntimeSystem,
		"volume":    checkContainerRuntimeVolume,
		"container": checkContainerRuntimeContainer,
		"rm":        checkContainerRuntimeRemove,
		"remove":    checkContainerRuntimeRemove,
		"compose":   checkContainerRuntimeCompose,
	}
}

func checkContainerRuntimeSystem(verb string, args []string) (RiskLevel, string, bool) {
	if len(args) > 1 && args[1] == "prune" {
		return RiskDangerous, verb + " system prune — deletes local container runtime state", true
	}
	return 0, "", false
}

func checkContainerRuntimeVolume(verb string, args []string) (RiskLevel, string, bool) {
	if len(args) > 1 && isContainerRemovalSubcommand(args[1]) {
		return RiskDangerous, verb + " volume " + args[1] + " — deletes local volume data", true
	}
	return 0, "", false
}

func checkContainerRuntimeContainer(verb string, args []string) (RiskLevel, string, bool) {
	if len(args) > 1 && isContainerRemovalSubcommand(args[1]) {
		return RiskDangerous, verb + " container " + args[1] + " — deletes local containers", true
	}
	return 0, "", false
}

func checkContainerRuntimeRemove(verb string, args []string) (RiskLevel, string, bool) {
	return RiskDangerous, verb + " " + args[0] + " — deletes local containers", true
}

func checkContainerRuntimeCompose(verb string, args []string) (RiskLevel, string, bool) {
	if len(args) > 1 && args[1] == "down" && hasAnyToken(args[2:], "-v", "--volumes") {
		return RiskDangerous, verb + " compose down --volumes — deletes local volume data", true
	}
	return 0, "", false
}

func containerRuntimePayload(tokens []string) []string {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			if i+1 < len(tokens) {
				return tokens[i+1:]
			}
			return nil
		}
		if containerRuntimeGlobalFlagConsumesArg(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tokens[i:]
	}
	return nil
}

func containerRuntimeGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "--config", "--context", "--host", "-H", "--log-level", "--tlscacert", "--tlscert", "--tlskey":
		return true
	default:
		return false
	}
}

func isContainerRemovalSubcommand(tok string) bool {
	return tok == "rm" || tok == "remove" || tok == "prune"
}

func hasAnyToken(tokens []string, values ...string) bool {
	for _, tok := range tokens {
		for _, value := range values {
			if tok == value {
				return true
			}
		}
	}
	return false
}

func checkKubectlDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := clusterToolPayload(tokens[1:])
	if len(args) > 0 && (args[0] == "delete" || args[0] == "drain") {
		return RiskDangerous, "kubectl " + args[0] + " — removes or evicts cluster resources", true
	}
	return 0, "", false
}

func checkHelmDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := clusterToolPayload(tokens[1:])
	if len(args) > 0 && (args[0] == "uninstall" || args[0] == "delete") {
		return RiskDangerous, "helm " + args[0] + " — removes cluster releases", true
	}
	return 0, "", false
}

func clusterToolPayload(tokens []string) []string {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			if i+1 < len(tokens) {
				return tokens[i+1:]
			}
			return nil
		}
		if clusterToolGlobalFlagConsumesArg(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tokens[i:]
	}
	return nil
}

func clusterToolGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "-n", "--namespace", "--context", "--kubeconfig", "--server", "--user", "--as", "--as-group":
		return true
	default:
		return false
	}
}

func checkTerraformDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := terraformPayload(tokens[1:])
	if len(args) == 0 {
		return 0, "", false
	}
	if args[0] == "destroy" {
		return RiskDangerous, tokens[0] + " destroy — deletes managed infrastructure", true
	}
	if args[0] == "apply" && hasAnyToken(args[1:], "-destroy") {
		return RiskDangerous, tokens[0] + " apply -destroy — deletes managed infrastructure", true
	}
	return 0, "", false
}

func terraformPayload(tokens []string) []string {
	return payloadAfterGlobalFlags(tokens, terraformGlobalFlagConsumesArg)
}

func terraformGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "-chdir":
		return true
	default:
		return false
	}
}

func checkPulumiDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := pulumiPayload(tokens[1:])
	if len(args) == 0 {
		return 0, "", false
	}
	if args[0] == "destroy" {
		return RiskDangerous, "pulumi destroy — deletes managed infrastructure", true
	}
	if len(args) > 1 && args[0] == "stack" && (args[1] == "rm" || args[1] == "remove") {
		return RiskDangerous, "pulumi stack " + args[1] + " — removes stack state", true
	}
	return 0, "", false
}

func pulumiPayload(tokens []string) []string {
	return payloadAfterGlobalFlags(tokens, pulumiGlobalFlagConsumesArg)
}

func pulumiGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "-C", "--cwd", "--config-file", "--profiling", "-s", "--stack":
		return true
	default:
		return false
	}
}

func checkGitHubCLIDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := githubCLIPayload(tokens[1:])
	if len(args) == 0 {
		return 0, "", false
	}
	if len(args) > 1 && (args[1] == "delete" || args[1] == "delete-asset") {
		switch args[0] {
		case "repo", "release", "run", "workflow":
			return RiskDangerous, "gh " + args[0] + " " + args[1] + " — deletes remote GitHub resources", true
		}
	}
	if args[0] == "api" && githubAPIUsesDelete(args[1:]) {
		return RiskDangerous, "gh api DELETE — deletes remote GitHub resources", true
	}
	return 0, "", false
}

func githubCLIPayload(tokens []string) []string {
	return payloadAfterGlobalFlags(tokens, githubCLIGlobalFlagConsumesArg)
}

func githubCLIGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "-R", "--repo", "--hostname":
		return true
	default:
		return false
	}
}

func githubAPIUsesDelete(tokens []string) bool {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "-X" || tok == "--method" {
			if i+1 < len(tokens) && strings.EqualFold(tokens[i+1], "DELETE") {
				return true
			}
			i++
			continue
		}
		if strings.HasPrefix(tok, "-X") && len(tok) > len("-X") && strings.EqualFold(tok[len("-X"):], "DELETE") {
			return true
		}
		if strings.HasPrefix(tok, "--method=") && strings.EqualFold(strings.TrimPrefix(tok, "--method="), "DELETE") {
			return true
		}
	}
	return false
}

func checkPackageManagerDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := packageManagerPayload(tokens)
	if len(args) == 0 {
		return 0, "", false
	}
	verb := tokens[0]
	if verb == "pacman" {
		return checkPacmanDestructiveArgs(args)
	}
	if isPackageRemovalSubcommand(args[0]) {
		return RiskDangerous, verb + " " + args[0] + " — removes installed packages", true
	}
	return 0, "", false
}

func packageManagerPayload(tokens []string) []string {
	switch tokens[0] {
	case "brew":
		return payloadAfterGlobalFlags(tokens[1:], brewGlobalFlagConsumesArg)
	case "apt", "apt-get", "dnf", "yum":
		return payloadAfterGlobalFlags(tokens[1:], linuxPackageGlobalFlagConsumesArg)
	case "pacman":
		return tokens[1:]
	case "npm", "pnpm", "yarn", "bun", "pip", "pip3", "gem", "cargo", "composer":
		return payloadAfterGlobalFlags(tokens[1:], languagePackageGlobalFlagConsumesArg)
	default:
		return nil
	}
}

func brewGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "--prefix", "--repository", "--cellar":
		return true
	default:
		return false
	}
}

func linuxPackageGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "-c", "-o", "--config-file", "--option", "--installroot", "--releasever":
		return true
	default:
		return false
	}
}

func languagePackageGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "-C", "--cwd", "--prefix", "--project", "--python":
		return true
	default:
		return false
	}
}

func isPackageRemovalSubcommand(subcommand string) bool {
	switch subcommand {
	case "uninstall", "remove", "rm", "purge", "autoremove", "erase":
		return true
	default:
		return false
	}
}

func checkPacmanDestructiveArgs(args []string) (RiskLevel, string, bool) {
	for _, tok := range args {
		if isPacmanRemovalFlag(tok) {
			return RiskDangerous, "pacman " + tok + " — removes installed packages", true
		}
	}
	return 0, "", false
}

func isPacmanRemovalFlag(tok string) bool {
	if tok == "-R" || strings.HasPrefix(tok, "-R") {
		return true
	}
	if strings.HasPrefix(tok, "--remove") {
		return true
	}
	return false
}

func checkAWSDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := awsPayload(tokens[1:])
	if len(args) < 2 {
		return 0, "", false
	}
	service, operation := args[0], args[1]
	if service == "s3" && (operation == "rm" || operation == "rb") {
		return RiskDangerous, "aws s3 " + operation + " — deletes S3 data or buckets", true
	}
	if isCloudDestructiveOperation(operation) {
		return RiskDangerous, "aws " + service + " " + operation + " — deletes or terminates cloud resources", true
	}
	return 0, "", false
}

func awsPayload(tokens []string) []string {
	return payloadAfterGlobalFlags(tokens, awsGlobalFlagConsumesArg)
}

func awsGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "--ca-bundle", "--cli-binary-format", "--cli-input-json", "--cli-input-yaml",
		"--color", "--endpoint-url", "--output", "--profile", "--query", "--region":
		return true
	default:
		return false
	}
}

func checkAzureDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := azurePayload(tokens[1:])
	for _, tok := range positionalTokens(args, azureOptionConsumesArg) {
		if isCloudDestructiveOperation(tok) {
			return RiskDangerous, "az " + tok + " — deletes or purges cloud resources", true
		}
	}
	return 0, "", false
}

func azurePayload(tokens []string) []string {
	return payloadAfterGlobalFlags(tokens, azureGlobalFlagConsumesArg)
}

func azureGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "--cloud", "--defaults", "--only-show-errors", "--output", "--profile",
		"--query", "--subscription", "--tenant", "--username":
		return true
	default:
		return false
	}
}

func azureOptionConsumesArg(tok string) bool {
	if azureGlobalFlagConsumesArg(tok) {
		return true
	}
	switch tok {
	case "--ids", "--location", "--name", "--resource-group", "--slot", "-g", "-n":
		return true
	default:
		return false
	}
}

func checkGcloudDestructiveArgs(tokens []string) (RiskLevel, string, bool) {
	args := gcloudPayload(tokens[1:])
	for _, tok := range positionalTokens(args, gcloudOptionConsumesArg) {
		if isCloudDestructiveOperation(tok) {
			return RiskDangerous, "gcloud " + tok + " — deletes or destroys cloud resources", true
		}
	}
	return 0, "", false
}

func gcloudPayload(tokens []string) []string {
	return payloadAfterGlobalFlags(tokens, gcloudGlobalFlagConsumesArg)
}

func gcloudGlobalFlagConsumesArg(tok string) bool {
	switch tok {
	case "--account", "--billing-project", "--configuration", "--flags-file",
		"--format", "--impersonate-service-account", "--project", "--trace-token":
		return true
	default:
		return false
	}
}

func gcloudOptionConsumesArg(tok string) bool {
	if gcloudGlobalFlagConsumesArg(tok) {
		return true
	}
	switch tok {
	case "--cluster", "--instance", "--name", "--region", "--zone":
		return true
	default:
		return false
	}
}

func payloadAfterGlobalFlags(tokens []string, consumesArg func(string) bool) []string {
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			if i+1 < len(tokens) {
				return tokens[i+1:]
			}
			return nil
		}
		if consumesArg(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tokens[i:]
	}
	return nil
}

func positionalTokens(tokens []string, consumesArg func(string) bool) []string {
	var values []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--" {
			values = append(values, tokens[i+1:]...)
			break
		}
		if consumesArg(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		values = append(values, tok)
	}
	return values
}

func isCloudDestructiveOperation(operation string) bool {
	return strings.HasPrefix(operation, "delete-") ||
		strings.HasPrefix(operation, "remove-") ||
		strings.HasPrefix(operation, "terminate-") ||
		operation == "delete" ||
		operation == "destroy" ||
		operation == "purge" ||
		operation == "remove" ||
		operation == "terminate"
}

func checkSQLCommandArgs(tokens []string) (RiskLevel, string, bool) {
	for _, sql := range inlineSQLStatements(tokens) {
		if isDestructiveSQL(sql) {
			return RiskDangerous, tokens[0] + " destructive SQL — can delete persistent data", true
		}
	}
	return 0, "", false
}

func inlineSQLStatements(tokens []string) []string {
	switch tokens[0] {
	case "psql":
		return optionValues(tokens[1:], "-c", "--command")
	case "mysql":
		return optionValues(tokens[1:], "-e", "--execute")
	case "sqlite3":
		return sqliteInlineStatements(tokens[1:])
	default:
		return nil
	}
}

func optionValues(tokens []string, shortFlag, longFlag string) []string {
	var values []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == shortFlag || tok == longFlag:
			if i+1 < len(tokens) {
				values = append(values, tokens[i+1])
				i++
			}
		case strings.HasPrefix(tok, shortFlag) && len(tok) > len(shortFlag):
			values = append(values, tok[len(shortFlag):])
		case strings.HasPrefix(tok, longFlag+"="):
			values = append(values, strings.TrimPrefix(tok, longFlag+"="))
		}
	}
	return values
}

func sqliteInlineStatements(tokens []string) []string {
	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if i+1 < len(tokens) {
			return tokens[i+1:]
		}
		return nil
	}
	return nil
}

func isDestructiveSQL(sql string) bool {
	sql = strings.TrimSpace(sql)
	sql = strings.TrimLeft(sql, "({")
	upper := strings.ToUpper(sql)
	destructivePrefixes := []string{
		"DROP ",
		"DROP\n",
		"TRUNCATE ",
		"TRUNCATE\n",
		"DELETE ",
		"DELETE\n",
	}
	for _, prefix := range destructivePrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return upper == "DROP" || upper == "TRUNCATE" || upper == "DELETE"
}

func checkAliasDefinitions(tokens []string) (RiskLevel, string, bool) {
	maxLevel := RiskSafe
	var maxReason string
	for _, tok := range tokens[1:] {
		name, value, ok := strings.Cut(tok, "=")
		if !ok || name == "" || value == "" {
			continue
		}
		level, reason := ClassifyCommand(value)
		if level > maxLevel {
			maxLevel = level
			maxReason = reason
		}
	}
	if maxLevel > RiskSafe {
		return maxLevel, "alias definition: " + maxReason, true
	}
	return 0, "", false
}

func checkHashBindings(tokens []string) (RiskLevel, string, bool) {
	for i := 1; i < len(tokens); i++ {
		if tokens[i] != "-p" || i+1 >= len(tokens) {
			continue
		}
		level, reason := ClassifyCommand(tokens[i+1])
		if level > RiskSafe {
			return level, "hash binding: " + reason, true
		}
		i += 2
	}
	return 0, "", false
}

func checkTeeTargets(tokens []string) (RiskLevel, string, bool) {
	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if isDangerousDevicePath(tok) {
			return RiskDangerous, "tee to device " + tok + " — can destroy data", true
		}
	}
	return 0, "", false
}
