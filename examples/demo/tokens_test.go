package main

import (
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"hello world", "hello world", 2},           // len=11, 11/4=2
		{"four chars", "four", 1},                   // len=4, 4/4=1
		{"single char", "x", 0},                     // len=1, 1/4=0
		{"eight chars", "12345678", 2},              // len=8, 8/4=2
		{"twenty chars", "12345678901234567890", 5}, // len=20, 20/4=5
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EstimateTokens(tc.input)
			if got != tc.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestEstimateMessagesTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		messages []message
		want     int
	}{
		{
			name:     "empty slice",
			messages: []message{},
			want:     0,
		},
		{
			name: "single message empty content",
			// overhead=4, content tokens=0 → 4
			messages: []message{{Role: "user", Content: ""}},
			want:     4,
		},
		{
			name: "two messages",
			// msg1: overhead=4, "hello world" (len=11) → 11/4=2 → 6
			// msg2: overhead=4, "four" (len=4) → 4/4=1 → 5
			// total = 11
			messages: []message{
				{Role: "user", Content: "hello world"},
				{Role: "assistant", Content: "four"},
			},
			want: 11,
		},
		{
			name: "system message with tool call overhead",
			// overhead=4, "1234567890" (len=10) → 10/4=2 → 6
			messages: []message{{Role: "system", Content: "1234567890"}},
			want:     6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EstimateMessagesTokens(tc.messages)
			if got != tc.want {
				t.Errorf("EstimateMessagesTokens(%v) = %d, want %d", tc.messages, got, tc.want)
			}
		})
	}
}

func TestShouldCompact(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		tokenCount int
		window     int
		threshold  float64
		want       bool
	}{
		{"above threshold", 7000, 8192, 0.70, true},  // 7000/8192=0.854 >= 0.70
		{"below threshold", 3000, 8192, 0.70, false}, // 3000/8192=0.366 < 0.70
		{"zero window disabled", 100, 0, 0.70, false},
		{"zero threshold disabled", 100, 8192, 0, false},
		{"just below threshold", 5734, 8192, 0.70, false}, // 5734/8192=0.6999... < 0.70
		{"just at threshold", 5735, 8192, 0.70, true},     // 5735/8192=0.70007... >= 0.70
		{"negative window disabled", 100, -1, 0.70, false},
		{"negative threshold disabled", 100, 8192, -0.5, false},
		{"full window", 8192, 8192, 0.70, true},
		{"empty context", 0, 8192, 0.70, false}, // 0/8192=0 < 0.70
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ShouldCompact(tc.tokenCount, tc.window, tc.threshold)
			if got != tc.want {
				t.Errorf("ShouldCompact(%d, %d, %v) = %v, want %v",
					tc.tokenCount, tc.window, tc.threshold, got, tc.want)
			}
		})
	}
}

func TestFormatCompactLog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		before int
		after  int
		want   string
	}{
		{
			name:   "typical reduction",
			before: 1000,
			after:  500,
			want:   "✓ history compacted · 1000→500 tokens · 50% reduction",
		},
		{
			name:   "no reduction same count",
			before: 1000,
			after:  1000,
			want:   "✓ history compacted · 1000→1000 tokens · 0% reduction",
		},
		{
			name:   "zero before guard",
			before: 0,
			after:  100,
			want:   "",
		},
		{
			name:   "after greater than before",
			before: 1000,
			after:  1247,
			want:   "✓ history compacted · 1000→1247 tokens · 0% reduction",
		},
		{
			name:   "62 percent reduction",
			before: 1247,
			after:  474, // 1247*0.38=473.86 → after=474, reduction=(1247-474)/1247=62%
			want:   "✓ history compacted · 1247→474 tokens · 61% reduction",
		},
		{
			name:   "negative before guard",
			before: -1,
			after:  100,
			want:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FormatCompactLog(tc.before, tc.after)
			if got != tc.want {
				t.Errorf("FormatCompactLog(%d, %d) =\n  %q\nwant\n  %q", tc.before, tc.after, got, tc.want)
			}
		})
	}
}
