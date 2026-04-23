package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// envDefault returns the value of key from the environment, or fallback when
// the variable is unset or empty.
func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envIntDefault parses key as a positive integer; returns fallback on parse
// error, empty env, or non-positive value.
func envIntDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

// envIntDefaultAllowZero is envIntDefault but permits zero as a valid value.
// Used where zero has semantic meaning (e.g., "disabled").
func envIntDefaultAllowZero(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
		return fallback
	}
	return n
}

// envFloatDefault parses key as a float64; returns def on parse error or empty env.
func envFloatDefault(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// envBoolDefault parses key as a boolean; accepts 1/true/yes/on and 0/false/no/off.
// Returns fallback on empty env or unrecognised value.
func envBoolDefault(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// firstNonEmpty returns the first non-empty string from vs, or "" if all are empty.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
