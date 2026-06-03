package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// inferValueType detects the stored value type from a string (ported from vybe
// store/memory_helpers.go). Detection order: boolean → number → array/json → string.
func inferValueType(value string) string {
	v := strings.TrimSpace(value)

	if v == "true" || v == "false" {
		return "boolean"
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return "number"
	}
	if len(v) >= 2 {
		if v[0] == '[' && v[len(v)-1] == ']' {
			var js interface{}
			if json.Unmarshal([]byte(v), &js) == nil {
				if _, ok := js.([]interface{}); ok {
					return "array"
				}
			}
		}
		if v[0] == '{' && v[len(v)-1] == '}' {
			var js interface{}
			if json.Unmarshal([]byte(v), &js) == nil {
				if _, ok := js.(map[string]interface{}); ok {
					return "json"
				}
			}
		}
	}
	return "string"
}

// boolToInt converts bool to SQLite integer (1/0).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseExpiresIn parses a duration string (e.g. "30d", "12h", "2w") and
// returns the corresponding expiration time. Returns nil, nil for empty input.
// Ported from vybe internal/actions/memory.go ParseExpiresIn.
func parseExpiresIn(duration string) (*time.Time, error) {
	if duration == "" {
		return nil, nil
	}
	d, err := parseDurationExtended(duration)
	if err != nil {
		return nil, fmt.Errorf("invalid expires_in: %w", err)
	}
	t := time.Now().Add(d)
	return &t, nil
}

func parseDurationExtended(input string) (time.Duration, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	last := s[len(s)-1]
	if last != 'd' && last != 'w' {
		return 0, fmt.Errorf("unsupported duration: %q (use e.g. 30d, 2w, 12h)", input)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s[:len(s)-1]), 10, 64)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("duration must be positive")
	}
	if last == 'w' {
		n *= 7
	}
	return time.Duration(n) * 24 * time.Hour, nil
}
