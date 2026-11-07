package jinn

import (
	"strings"
)

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
	case "-C", "--cwd", "--dir", "--prefix", "--project", "--python":
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
