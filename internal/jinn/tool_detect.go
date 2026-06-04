package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type projectInfo struct {
	Languages   []string `json:"languages"`
	BuildTool   string   `json:"build_tool,omitempty"`
	TestCommand string   `json:"test_command,omitempty"`
	Linter      string   `json:"linter,omitempty"`
	ConfigFiles []string `json:"config_files"`
	Frameworks  []string `json:"frameworks,omitempty"`
}

type probe struct {
	configFile string
	language   string
	buildTool  string
	testCmd    string
	linter     string
}

var probes = []probe{
	{"go.mod", "Go", "go build", "go test ./...", "go vet"},
	{"package.json", "JavaScript", "npm", "npm test", "npx eslint"},
	{"bun.lockb", "TypeScript", "bun", "bun test", ""},
	{"Cargo.toml", "Rust", "cargo build", "cargo test", "cargo clippy"},
	{"pyproject.toml", "Python", "pip", "pytest", "ruff check"},
	{"setup.py", "Python", "pip", "pytest", "ruff check"},
	{"requirements.txt", "Python", "pip", "pytest", ""},
	{"composer.json", "PHP", "composer", "phpunit", ""},
	{"Makefile", "", "make", "make test", ""},
	{"Taskfile.yml", "", "task", "task test", ""},
}

func (e *Engine) detectProject(args map[string]interface{}) (string, error) {
	detectPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		detectPath = p
	}

	resolved, err := e.checkPath(detectPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", detectPath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", detectPath)
	}

	result := projectInfo{}
	for _, p := range probes {
		probeMarker(resolved, p, &result)
	}

	applyTypeScriptSignal(resolved, &result)
	applyPackageScripts(resolved, &result)
	applyJustfile(resolved, &result)
	applyFrameworks(resolved, &result)

	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// probeMarker records a marker file's signals when it exists in dir.
func probeMarker(dir string, p probe, result *projectInfo) {
	if _, statErr := os.Stat(filepath.Join(dir, p.configFile)); statErr != nil {
		return
	}
	result.ConfigFiles = append(result.ConfigFiles, p.configFile)
	if p.language != "" {
		result.Languages = append(result.Languages, p.language)
	}
	if result.BuildTool == "" && p.buildTool != "" {
		result.BuildTool = p.buildTool
		result.TestCommand = p.testCmd
		result.Linter = p.linter
	}
}

// applyTypeScriptSignal upgrades JavaScript to TypeScript when tsconfig.json exists.
func applyTypeScriptSignal(dir string, result *projectInfo) {
	if _, statErr := os.Stat(filepath.Join(dir, "tsconfig.json")); statErr != nil {
		return
	}
	for i, lang := range result.Languages {
		if lang == "JavaScript" {
			result.Languages[i] = "TypeScript"
		}
	}
}

// applyPackageScripts reads package.json scripts and overrides build/test/lint.
func applyPackageScripts(dir string, result *projectInfo) {
	data, rErr := os.ReadFile(filepath.Join(dir, "package.json"))
	if rErr != nil {
		return
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return
	}
	if _, ok := pkg.Scripts["test"]; ok {
		result.TestCommand = "npm run test"
	}
	if _, ok := pkg.Scripts["lint"]; ok {
		result.Linter = "npm run lint"
	}
	if _, ok := pkg.Scripts["build"]; ok {
		result.BuildTool = "npm run build"
	}
}

// applyJustfile prefers committed just recipes as the repo's source of truth
// for build/test commands.
func applyJustfile(dir string, result *projectInfo) {
	name, data, ok := readJustfile(dir)
	if !ok {
		return
	}
	if !slices.Contains(result.ConfigFiles, name) {
		result.ConfigFiles = append(result.ConfigFiles, name)
	}
	recipes := parseJustRecipes(string(data))
	if recipes["build"] {
		result.BuildTool = "just build"
	}
	if recipes["test"] {
		result.TestCommand = "just test"
	}
	if recipes["lint"] {
		result.Linter = "just lint"
	}
}

// applyFrameworks detects frameworks by config files; accepts either extension, adds once.
func applyFrameworks(dir string, result *projectInfo) {
	for _, cfg := range []string{"next.config.js", "next.config.mjs"} {
		if _, statErr := os.Stat(filepath.Join(dir, cfg)); statErr == nil {
			result.Frameworks = append(result.Frameworks, "Next.js")
			break
		}
	}
}

func readJustfile(dir string) (string, []byte, bool) {
	for _, name := range []string{"justfile", "Justfile"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return name, data, true
		}
	}
	return "", nil, false
}

func parseJustRecipes(content string) map[string]bool {
	recipes := make(map[string]bool)
	for line := range strings.SplitSeq(content, "\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, ":=") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(name)
		if len(fields) > 0 {
			recipes[fields[0]] = true
		}
	}
	return recipes
}
