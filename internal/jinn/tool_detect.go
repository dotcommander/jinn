package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
