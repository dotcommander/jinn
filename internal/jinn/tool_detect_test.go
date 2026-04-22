package jinn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProject_Go(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.22\n")

	result, err := e.detectProject(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	if err := json.Unmarshal([]byte(result), &info); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(info.Languages) == 0 || info.Languages[0] != "Go" {
		t.Errorf("expected Go language, got %v", info.Languages)
	}
	if info.TestCommand != "go test ./..." {
		t.Errorf("expected 'go test ./...', got %q", info.TestCommand)
	}
	if info.Linter != "go vet" {
		t.Errorf("expected 'go vet', got %q", info.Linter)
	}
}

func TestDetectProject_NodeJS(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "package.json", `{"name": "test", "scripts": {"test": "jest"}}`)

	result, err := e.detectProject(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	json.Unmarshal([]byte(result), &info)
	found := false
	for _, lang := range info.Languages {
		if lang == "JavaScript" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected JavaScript language, got %v", info.Languages)
	}
}

func TestDetectProject_TypeScript(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "package.json", `{"name": "test"}`)
	writeTestFile(t, dir, "tsconfig.json", `{"compilerOptions": {}}`)

	result, err := e.detectProject(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	json.Unmarshal([]byte(result), &info)
	found := false
	for _, lang := range info.Languages {
		if lang == "TypeScript" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TypeScript language, got %v", info.Languages)
	}
}

func TestDetectProject_Python(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "pyproject.toml", "[project]\nname = 'test'\n")

	result, err := e.detectProject(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	json.Unmarshal([]byte(result), &info)
	if len(info.Languages) == 0 || info.Languages[0] != "Python" {
		t.Errorf("expected Python language, got %v", info.Languages)
	}
}

func TestDetectProject_Rust(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "Cargo.toml", "[package]\nname = 'test'\nversion = '0.1.0'\n")

	result, err := e.detectProject(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	json.Unmarshal([]byte(result), &info)
	if len(info.Languages) == 0 || info.Languages[0] != "Rust" {
		t.Errorf("expected Rust language, got %v", info.Languages)
	}
}

func TestDetectProject_EmptyDir(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	sub := filepath.Join(dir, "empty")
	os.MkdirAll(sub, 0755)

	result, err := e.detectProject(args("path", "empty"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	json.Unmarshal([]byte(result), &info)
	if len(info.Languages) != 0 {
		t.Errorf("expected no languages for empty dir, got %v", info.Languages)
	}
}

func TestDetectProject_PackageJSONScripts(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "package.json", `{
		"name": "test",
		"scripts": {
			"test": "vitest run",
			"lint": "eslint .",
			"build": "tsc"
		}
	}`)

	result, err := e.detectProject(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var info projectInfo
	json.Unmarshal([]byte(result), &info)
	if info.TestCommand != "npm run test" {
		t.Errorf("expected 'npm run test', got %q", info.TestCommand)
	}
	if info.Linter != "npm run lint" {
		t.Errorf("expected 'npm run lint', got %q", info.Linter)
	}
}
