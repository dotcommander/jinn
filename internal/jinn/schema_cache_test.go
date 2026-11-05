package jinn

import (
	"strings"
	"testing"
)

func TestParsedSchemaIsShared(t *testing.T) {
	t.Parallel()
	a, err := parsedSchemaTools()
	if err != nil {
		t.Fatal(err)
	}
	b, err := parsedSchemaTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) == 0 || &a[0] != &b[0] {
		t.Fatal("parsed schema slice is not shared across calls")
	}
}

func TestLeanSchemaModeIsolation(t *testing.T) {
	t.Parallel()
	disabled, err := LeanSchemaForMode(ShellModeDisabled)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := LeanSchemaForMode(ShellModeUnsafe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(disabled, `"name":"run_shell"`) {
		t.Fatal("disabled lean schema must exclude run_shell")
	}
	if !strings.Contains(enabled, `"name":"run_shell"`) {
		t.Fatal("unsafe lean schema must include run_shell")
	}
}
