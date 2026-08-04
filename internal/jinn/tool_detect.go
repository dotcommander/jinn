package jinn

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
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

	info, err := e.rootedStat(resolved)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", detectPath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", detectPath)
	}

	result := projectInfo{}
	for _, p := range probes {
		e.probeMarker(resolved, p, &result)
	}

	e.applyTypeScriptSignal(resolved, &result)
	e.applyPackageScripts(resolved, &result)
	e.applyJustfile(resolved, &result)
	e.applyFrameworks(resolved, &result)

	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// probeMarker records a marker file's signals when it exists in dir.
func (e *Engine) probeMarker(dir string, p probe, result *projectInfo) {
	data, ok := e.readProjectMarker(dir, p.configFile)
	if !ok || !validProjectMarker(p.configFile, data) {
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
func (e *Engine) applyTypeScriptSignal(dir string, result *projectInfo) {
	if data, ok := e.readProjectMarker(dir, "tsconfig.json"); !ok || !validProjectMarker("tsconfig.json", data) {
		return
	}
	for i, lang := range result.Languages {
		if lang == "JavaScript" {
			result.Languages[i] = "TypeScript"
		}
	}
}

// applyPackageScripts reads package.json scripts and overrides build/test/lint.
func (e *Engine) applyPackageScripts(dir string, result *projectInfo) {
	data, ok := e.readProjectMarker(dir, "package.json")
	if !ok {
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
func (e *Engine) applyJustfile(dir string, result *projectInfo) {
	name, data, ok := e.readJustfile(dir)
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
func (e *Engine) applyFrameworks(dir string, result *projectInfo) {
	for _, cfg := range []string{"next.config.js", "next.config.mjs"} {
		if _, ok := e.readProjectMarker(dir, cfg); ok {
			result.Frameworks = append(result.Frameworks, "Next.js")
			break
		}
	}
}

func (e *Engine) readJustfile(dir string) (string, []byte, bool) {
	for _, name := range []string{"justfile", "Justfile"} {
		data, ok := e.readProjectMarker(dir, name)
		if ok {
			return name, data, true
		}
	}
	return "", nil, false
}

const projectMarkerMaxBytes = 1 << 20

func (e *Engine) readProjectMarker(dir, name string) ([]byte, bool) {
	resolved := filepath.Join(dir, name)
	data, info, err := e.rootedReadFile(resolved, projectMarkerMaxBytes)
	return data, err == nil && info.Mode().IsRegular()
}

var goModuleLine = regexp.MustCompile(`(?m)^module[ \t]+[^ \t\r\n]+[ \t]*$`)

func validProjectMarker(name string, data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	switch name {
	case "go.mod":
		return goModuleLine.Match(data)
	case "package.json", "composer.json":
		var value map[string]any
		return json.Unmarshal(data, &value) == nil && value != nil
	case "Cargo.toml", "pyproject.toml":
		return strings.Contains(trimmed, "[") && strings.Contains(trimmed, "]")
	case "Taskfile.yml":
		return strings.Contains(trimmed, ":")
	case "tsconfig.json":
		return strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")
	default:
		return len(data) > 0
	}
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
