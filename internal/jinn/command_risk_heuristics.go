package jinn

import (
	"strings"
)

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
	// git diff, show, and log share diff output options. An output target
	// writes a file even though these subcommands are ordinarily read-only.
	if verb == "git" && (subcommand == "diff" || subcommand == "show" || subcommand == "log") {
		for _, tok := range tokens[1:] {
			if tok == "--" {
				break // Remaining arguments are pathspecs, not options.
			}
			if tok == "--output" || strings.HasPrefix(tok, "--output=") {
				return RiskCaution, "git --output — writes diff output to a file", true
			}
		}
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
