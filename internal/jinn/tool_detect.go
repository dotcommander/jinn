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

func (e *Engine) detectProject(args map[string]any) (string, error) {
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
		if _, err := os.Stat(filepath.Join(resolved, p.configFile)); err == nil {
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
	}

	// Secondary signals
	if _, err := os.Stat(filepath.Join(resolved, "tsconfig.json")); err == nil {
		for i, lang := range result.Languages {
			if lang == "JavaScript" {
				result.Languages[i] = "TypeScript"
			}
		}
	}

	// Read package.json scripts if present
	pkgPath := filepath.Join(resolved, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
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
	}

	// Prefer committed just recipes when present; they are usually the repo's
	// strongest source of truth for build/test commands.
	if name, data, ok := readJustfile(resolved); ok {
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

	// Framework detection: accept either config extension, add once.
	for _, cfg := range []string{"next.config.js", "next.config.mjs"} {
		if _, err := os.Stat(filepath.Join(resolved, cfg)); err == nil {
			result.Frameworks = append(result.Frameworks, "Next.js")
			break
		}
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
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
