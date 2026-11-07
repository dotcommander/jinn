package jinn

import (
	"path"
	"strings"
)

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
	for _, target := range commandOptionTargets(tokens[1:], curlFileWritingOptions...) {
		if isDangerousDevicePath(target) {
			return RiskDangerous, "curl output to device " + target + " — can destroy data", true
		}
	}
	for _, target := range curlWriteOutTargets(tokens[1:]) {
		if isDangerousDevicePath(target) {
			return RiskDangerous, "curl write-out to device " + target + " — can destroy data", true
		}
	}
	if target, ok := curlRemoteOutputDirectory(tokens[1:]); ok && isDangerousOutputDirectory(target) {
		return RiskDangerous, "curl remote output directory " + target + " — can destroy data", true
	}
	return 0, "", false
}

func checkWgetOutputTarget(tokens []string) (RiskLevel, string, bool) {
	for _, target := range commandOptionTargets(tokens[1:], wgetFileWritingOptions...) {
		if isDangerousDevicePath(target) {
			return RiskDangerous, "wget output to device " + target + " — can destroy data", true
		}
	}
	for _, target := range commandOptionTargets(tokens[1:], "-P", "--directory-prefix") {
		if isDangerousOutputDirectory(target) {
			return RiskDangerous, "wget output directory " + target + " — can destroy data", true
		}
	}
	return 0, "", false
}

func curlWriteOutTargets(tokens []string) []string {
	var targets []string
	for _, format := range commandOptionTargets(tokens, "-w", "--write-out") {
		for remaining := format; ; {
			start := strings.Index(remaining, "%output{")
			if start < 0 {
				break
			}
			remaining = remaining[start+len("%output{"):]
			end := strings.IndexByte(remaining, '}')
			if end < 0 {
				break
			}
			target := strings.TrimPrefix(remaining[:end], ">>")
			if target != "" {
				targets = append(targets, target)
			}
			remaining = remaining[end+1:]
		}
	}
	return targets
}

func curlRemoteOutputDirectory(tokens []string) (string, bool) {
	remoteName := false
	for _, tok := range tokens {
		if tok == "-O" || tok == "--remote-name" || tok == "--remote-name-all" {
			remoteName = true
			break
		}
	}
	if !remoteName {
		return "", false
	}
	targets := commandOptionTargets(tokens, "--output-dir")
	if len(targets) == 0 {
		return "", false
	}
	return targets[len(targets)-1], true
}

func isDangerousOutputDirectory(target string) bool {
	if strings.HasPrefix(target, "/") {
		target = path.Clean(target)
	}
	if target == "/dev/null" || target == "/dev/stdout" || target == "/dev/stderr" {
		return false
	}
	return target == "/dev" || strings.HasPrefix(target, "/dev/")
}

var curlFileWritingOptions = []string{
	"-o", "--output",
	"-D", "--dump-header",
	"--trace", "--trace-ascii",
	"--stderr",
	"-c", "--cookie-jar",
	"--hsts", "--alt-svc",
	"--etag-save", "--libcurl",
}

var wgetFileWritingOptions = []string{
	"-O", "--output-document",
	"-o", "--output-file",
	"-a", "--append-output",
	"--hsts-file",
	"--save-cookies", "--rejected-log",
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

// commandOptionTargets returns every value supplied to a short or long output
// option. Repeated options are valid for several command-line tools, and any
// one dangerous device target must not be masked by an earlier safe target.
func commandOptionTargets(tokens []string, options ...string) []string {
	var targets []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		for _, option := range options {
			switch {
			case tok == option:
				if i+1 < len(tokens) {
					targets = append(targets, tokens[i+1])
					i++
				}
			case strings.HasPrefix(option, "--") && strings.HasPrefix(tok, option+"="):
				targets = append(targets, strings.TrimPrefix(tok, option+"="))
			case !strings.HasPrefix(option, "--") && strings.HasPrefix(tok, option) && len(tok) > len(option):
				targets = append(targets, tok[len(option):])
			}
		}
	}
	return targets
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
