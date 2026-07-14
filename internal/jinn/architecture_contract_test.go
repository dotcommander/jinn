package jinn

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestArchitectureDocumentToolRegistry(t *testing.T) {
	doc := readArchitectureDocument(t)
	got := architectureToolConditions(t, doc)
	want := registeredToolNames()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("architecture tool conditions = %v, want registry names %v", got, want)
	}
}

func TestArchitectureDocumentFilesExist(t *testing.T) {
	doc := readArchitectureDocument(t)
	fileLine := regexp.MustCompile(`(?m)^\s+- ([A-Za-z0-9_./-]+\.go)\s*$`)
	for _, match := range fileLine.FindAllStringSubmatch(doc, -1) {
		path := filepath.Join("../..", match[1])
		if _, err := os.Stat(path); err != nil {
			t.Errorf("documented Go file %q: %v", match[1], err)
		}
	}
}

func TestArchitectureDocumentEntryModes(t *testing.T) {
	doc := readArchitectureDocument(t)
	for _, mode := range []string{"--inspect", "--mcp"} {
		if !strings.Contains(doc, `condition: "`+mode+`"`) {
			t.Errorf("entry modes missing %s", mode)
		}
	}
}

func readArchitectureDocument(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../..", "docs", "architecture.yaml"))
	if err != nil {
		t.Fatalf("read architecture document: %v", err)
	}
	return string(data)
}

func architectureToolConditions(t *testing.T, doc string) []string {
	t.Helper()
	condition := regexp.MustCompile(`condition: "([^"]+)"`)
	scanner := bufio.NewScanner(strings.NewReader(doc))
	inToolGate := false
	var names []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `gate: "tool name"`) {
			inToolGate = true
			continue
		}
		if !inToolGate {
			continue
		}
		if strings.HasPrefix(line, "      - fork:") {
			break
		}
		if match := condition.FindStringSubmatch(line); match != nil {
			names = append(names, match[1])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan architecture document: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("tool name gate has no conditions")
	}
	return names
}
