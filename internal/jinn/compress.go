package jinn

import (
	"fmt"
	"regexp"
	"strings"
)

// Strategy is a compression strategy that can be applied to tool output.
type Strategy interface {
	// Name returns a unique identifier for this strategy (used in metadata).
	Name() string
	// AppliesTo returns true if this strategy should run on the given output.
	// The tool name is provided so strategies can be tool-specific.
	AppliesTo(output string, tool string) bool
	// Compress applies the strategy to the output and returns the compressed version.
	// Must be deterministic: same input always produces same output.
	// Must be lossless for signal: never drop error messages, test failures, or diff hunks.
	Compress(output string) string
}

// CompressionMeta carries metadata about what compression was applied.
type CompressionMeta struct {
	Strategies  []string `json:"strategies,omitempty"`
	OriginalLen int      `json:"original_len,omitempty"`
	FinalLen    int      `json:"final_len,omitempty"`
}

// Compressor applies a chain of strategies to tool output.
type Compressor struct {
	strategies []Strategy
}

// NewCompressor creates a Compressor with the default strategy chain.
func NewCompressor() *Compressor {
	return &Compressor{
		strategies: []Strategy{
			&pathPrefixStrategy{},
			&hashAbbrevStrategy{},
			&testResultStrategy{},
			&buildOutputStrategy{},
			&gitStatusStrategy{},
		},
	}
}

// defaultCompressor is a shared, stateless Compressor used by run_shell.
var defaultCompressor = NewCompressor()

// Compress applies all applicable strategies to the output.
// It returns the compressed text and metadata about what was applied.
// If compression panics, the original output is returned (fail-open).
func (c *Compressor) Compress(output string, tool string) (result string, meta CompressionMeta) {
	meta.OriginalLen = len(output)

	// Fail-open: if any strategy panics, return the original output unchanged.
	defer func() {
		if r := recover(); r != nil {
			result = output
			meta = CompressionMeta{
				OriginalLen: len(output),
				FinalLen:    len(output),
			}
		}
	}()

	result = output
	for _, s := range c.strategies {
		if s.AppliesTo(result, tool) {
			result = s.Compress(result)
			meta.Strategies = append(meta.Strategies, s.Name())
		}
	}
	meta.FinalLen = len(result)
	return result, meta
}

// ---------------------------------------------------------------------------
// Compiled regex patterns (package-level to avoid per-call compilation).
// ---------------------------------------------------------------------------

// hashAbbrevStrategy: 40-character hex SHA hashes at word boundaries.
var reFullHash = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

// ---------------------------------------------------------------------------
// pathPrefixStrategy
// ---------------------------------------------------------------------------

// pathPrefixStrategy detects when 3+ lines share a common directory prefix
// and factors it out into a [cwd: ...] header line.
type pathPrefixStrategy struct{}

func (s *pathPrefixStrategy) Name() string { return "path_prefix_dedup" }

func (s *pathPrefixStrategy) AppliesTo(output string, tool string) bool {
	pathCount := 0
	for _, line := range splitLines(output) {
		if isPathLine(line) {
			pathCount++
			if pathCount >= 3 {
				return true
			}
		}
	}
	return false
}

func (s *pathPrefixStrategy) Compress(output string) string {
	lines := splitLines(output)
	if len(lines) == 0 {
		return output
	}

	// Collect trimmed path-like lines for prefix computation.
	var pathLines []string
	for _, line := range lines {
		t := strings.TrimLeft(line, " \t")
		if isPathStr(t) {
			pathLines = append(pathLines, t)
		}
	}
	if len(pathLines) < 3 {
		return output
	}

	prefix := longestCommonPathPrefix(pathLines)
	if prefix == "" {
		return output
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[cwd: %s]", strings.TrimSuffix(prefix, "/"))

	count := 0
	for _, line := range lines {
		b.WriteByte('\n')
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, prefix) {
			count++
			leading := line[:len(line)-len(t)]
			relative := t[len(prefix):]
			if relative == "" {
				relative = "."
			}
			b.WriteString(leading)
			b.WriteString(relative)
		} else {
			b.WriteString(line)
		}
	}

	if count < 3 {
		return output
	}
	result := b.String()
	if len(result) >= len(output) {
		return output
	}
	return result
}

// isPathLine returns true if the line starts with an absolute or relative path.
func isPathLine(line string) bool {
	return isPathStr(strings.TrimLeft(line, " \t"))
}

// isPathStr returns true if s looks like a file path (starts with / or ./).
func isPathStr(s string) bool {
	return (strings.HasPrefix(s, "/") && len(s) > 1) || strings.HasPrefix(s, "./")
}

// longestCommonPathPrefix finds the longest common directory prefix among
// the given paths. Returns empty string if no meaningful common prefix exists.
func longestCommonPathPrefix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	prefix := paths[0]
	for _, p := range paths[1:] {
		for !strings.HasPrefix(p, prefix) {
			idx := strings.LastIndex(prefix, "/")
			if idx <= 0 {
				return ""
			}
			prefix = prefix[:idx+1] // Keep trailing slash for directory boundary.
		}
		if prefix == "" {
			return ""
		}
	}
	// Ensure the prefix ends at a clean directory boundary.
	if !strings.HasSuffix(prefix, "/") {
		idx := strings.LastIndex(prefix, "/")
		if idx <= 0 {
			return ""
		}
		prefix = prefix[:idx+1]
	}
	// Reject trivially short prefixes.
	if prefix == "/" || prefix == "./" {
		return ""
	}
	return prefix
}

// ---------------------------------------------------------------------------
// hashAbbrevStrategy
// ---------------------------------------------------------------------------

// hashAbbrevStrategy abbreviates full 40-char hex SHA hashes to 8 characters.
type hashAbbrevStrategy struct{}

func (s *hashAbbrevStrategy) Name() string { return "hash_abbrev" }

func (s *hashAbbrevStrategy) AppliesTo(output string, tool string) bool {
	return len(reFullHash.FindAllString(output, -1)) >= 2
}

func (s *hashAbbrevStrategy) Compress(output string) string {
	result := reFullHash.ReplaceAllStringFunc(output, func(hash string) string {
		return hash[:8]
	})
	if len(result) >= len(output) {
		return output
	}
	return result
}
