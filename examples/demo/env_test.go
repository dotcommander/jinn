package main

import (
	"testing"
)

// Note: t.Setenv and t.Parallel do not mix — these tests run serially.

func TestEnvFloatDefault_ValidEnv(t *testing.T) {
	t.Setenv("DEMO_TEST_FLOAT", "0.42")
	got := envFloatDefault("DEMO_TEST_FLOAT", 0.7)
	if got != 0.42 {
		t.Errorf("envFloatDefault with valid env = %v, want 0.42", got)
	}
}

func TestEnvFloatDefault_InvalidEnv(t *testing.T) {
	t.Setenv("DEMO_TEST_FLOAT", "not-a-number")
	got := envFloatDefault("DEMO_TEST_FLOAT", 0.7)
	if got != 0.7 {
		t.Errorf("envFloatDefault with invalid env = %v, want 0.7 (default)", got)
	}
}

func TestEnvFloatDefault_EmptyEnv(t *testing.T) {
	t.Setenv("DEMO_TEST_FLOAT", "")
	got := envFloatDefault("DEMO_TEST_FLOAT", 0.7)
	if got != 0.7 {
		t.Errorf("envFloatDefault with empty env = %v, want 0.7 (default)", got)
	}
}

func TestPreprocessModel_EnvSet(t *testing.T) {
	t.Setenv("DEMO_PREPROCESS_MODEL", "openai/gpt-5.4-nano")
	got := envDefault("DEMO_PREPROCESS_MODEL", "")
	if got != "openai/gpt-5.4-nano" {
		t.Errorf("envDefault with env set = %q, want %q", got, "openai/gpt-5.4-nano")
	}
}

func TestPreprocessModel_EnvUnset(t *testing.T) {
	t.Setenv("DEMO_PREPROCESS_MODEL", "")
	got := envDefault("DEMO_PREPROCESS_MODEL", "")
	if got != "" {
		t.Errorf("envDefault with empty env = %q, want empty", got)
	}
}
