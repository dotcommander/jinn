package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var grepExcludeDirs = []string{".git", "node_modules", "vendor", "__pycache__", ".cache", "dist", "build"}

const searchDefaultMax = 500

// searchTimeout caps each rg/grep invocation so a slow filesystem scan cannot
// hang an agent tool call indefinitely. Declared as var so tests may shorten
// it; not part of the public API.
var searchTimeout = 60 * time.Second

// searchFiles is a context-less convenience shim used ONLY by tests; production
// Dispatch calls searchFilesContext(ctx, args) directly (engine.go). Do not add
// production callers — that would violate the Context Propagation rule.
func (e *Engine) searchFiles(args map[string]interface{}) (string, error) {
	return e.searchFilesContext(context.Background(), args)
}

func (e *Engine) searchFilesContext(ctx context.Context, args map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pattern, _ := args["pattern"].(string)
	searchPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = p
	}

	literal, _ := args["literal"].(bool)

	if _, err := e.checkPath(searchPath); err != nil {
		var sug *ErrWithSuggestion
		if errors.As(err, &sug) && sug.Code == "" {
			sug.Code = ErrCodePathOutsideSandbox
		}
		return "", err
	}
	if !literal {
		if _, err := regexp.Compile(pattern); err != nil {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("invalid regex: %w", err),
				Suggestion: "check your regex syntax — use literal:true for fixed-string search",
				Code:       ErrCodeInvalidRegex,
			}
		}
	}

	format := "text"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}

	req := searchRequest{
		pattern:    pattern,
		searchPath: searchPath,
		literal:    literal,
		// max_matches: default 500, 0 means use the default (not unlimited).
		maxMatches: intArg(args, "max_matches", searchDefaultMax),
	}
	req.cmd, req.cmdArgs = e.buildSearchArgs(args, literal)

	// Legacy max_results param (kept for backward compat).
	maxResults := intArg(args, "max_results", 0)

	if format == "filenames" {
		return e.searchFilenamesOnly(ctx, req)
	}

	raw, runErr := e.runSearch(ctx, req, maxResults)
	if runErr != nil {
		return "", runErr
	}

	if format == "json" {
		return e.formatSearchJSON(raw, req)
	}
	return e.formatSearchText(raw, req), nil
}

// searchRequest carries the resolved command and shared parameters for a single
// search across its run/parse/format phases.
type searchRequest struct {
	cmd        string
	cmdArgs    []string
	pattern    string
	searchPath string
	literal    bool
	maxMatches int
}

// runSearch appends the -m safety cap plus positional args, runs grep/rg, and
// returns the raw stdout (with the legacy max_results note appended if set).
func (e *Engine) runSearch(ctx context.Context, req searchRequest, maxResults int) (string, error) {
	cmdArgs := req.cmdArgs
	// Pass -m as a safety cap so grep stops scanning extremely large files
	// early. We use a generous cap (2× requested) rather than maxMatches directly
	// because -m limits per-file matches, and setting it to maxMatches would
	// break total_count accuracy when all matches reside in a single file.
	// The post-hoc Go cap is still needed for an accurate total_count.
	if req.maxMatches > 0 {
		safetyCap := req.maxMatches * 2
		if safetyCap < searchDefaultMax {
			safetyCap = searchDefaultMax
		}
		cmdArgs = append(cmdArgs, "-m", strconv.Itoa(safetyCap))
	}
	cmdArgs = append(cmdArgs, "--", req.pattern, req.searchPath)

	gr, runErr := e.runGrep(ctx, req.cmd, cmdArgs)
	if runErr != nil {
		return "", runErr
	}
	raw := gr.stdout
	if maxResults > 0 {
		raw += fmt.Sprintf("\n(results capped at max_results=%d, more matches may exist)", maxResults)
	}
	return raw, nil
}

// buildSearchArgs selects rg or grep and assembles the flag list common to all
// output formats (exclude dirs, include glob, literal, context_lines,
// case_insensitive). The pattern/path positional args and -m caps are appended
// by the caller.
func (e *Engine) buildSearchArgs(args map[string]interface{}, literal bool) (string, []string) {
	cmd, cmdArgs := e.searchEngineBaseArgs(args, literal)

	if ctx, ok := args["context_lines"].(float64); ok && int(ctx) > 0 {
		cmdArgs = append(cmdArgs, "-C", strconv.Itoa(int(ctx)))
	}
	if ci, ok := args["case_insensitive"].(bool); ok && ci {
		cmdArgs = append(cmdArgs, "-i")
	}
	return cmd, cmdArgs
}

// searchEngineBaseArgs resolves the rg-vs-grep binary and its base flags
// (exclude dirs, include glob, literal). Keeps the engine-specific branch in
// one place so buildSearchArgs stays flat.
func (e *Engine) searchEngineBaseArgs(args map[string]interface{}, literal bool) (string, []string) {
	include, _ := args["include"].(string)
	if e.rgPath != "" {
		cmdArgs := []string{"-n", "--no-heading"}
		for _, dir := range grepExcludeDirs {
			cmdArgs = append(cmdArgs, "--glob=!"+dir)
		}
		if include != "" {
			cmdArgs = append(cmdArgs, "--glob="+include)
		}
		if literal {
			cmdArgs = append(cmdArgs, "--fixed-strings")
		}
		return e.rgPath, cmdArgs
	}

	cmdArgs := []string{"-r", "-n"}
	for _, dir := range grepExcludeDirs {
		cmdArgs = append(cmdArgs, "--exclude-dir="+dir)
	}
	if include != "" {
		cmdArgs = append(cmdArgs, "--include="+include)
	}
	if literal {
		cmdArgs = append(cmdArgs, "-F")
	}
	return "grep", cmdArgs
}

// searchFilenamesOnly handles format=="filenames": per-file match counts via -c.
func (e *Engine) searchFilenamesOnly(ctx context.Context, req searchRequest) (string, error) {
	cmdArgs := req.cmdArgs
	if req.maxMatches > 0 {
		cmdArgs = append(cmdArgs, "-m", strconv.Itoa(req.maxMatches))
	}
	cmdArgs = append(cmdArgs, "-c", "--", req.pattern, req.searchPath)

	gr, runErr := e.runGrep(ctx, req.cmd, cmdArgs)
	if runErr != nil {
		return "", runErr
	}
	// Exit code 1 with empty stdout = no matches (not an error).
	// Any other non-zero exit with empty stdout is an error.
	if gr.stdout == "" && gr.exitCode != 0 && gr.exitCode != 1 {
		return "", fmt.Errorf("search failed: %s", gr.stderr)
	}
	return parseFilenamesOutput(gr.stdout, req.maxMatches), nil
}

// formatSearchJSON renders search output as the searchFilesResult JSON shape.
func (e *Engine) formatSearchJSON(raw string, req searchRequest) (string, error) {
	shown, total := parseSearchResults(raw, req.maxMatches)
	truncated := total > req.maxMatches

	resp := searchFilesResult{Results: shown, Truncated: truncated, TotalCount: total}
	if total == 0 {
		resp.ZeroMatchReason = e.classifyZeroMatch(req.pattern, req.searchPath, req.literal)
	}
	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("marshal search results: %w", err)
	}
	result := string(jsonBytes)
	if truncated {
		result += "\n" + formatTruncatedHint(req.maxMatches, total, "'max_matches' or a more specific pattern")
	}
	return result, nil
}

// formatSearchText renders search output as plain text with a line cap.
func (e *Engine) formatSearchText(raw string, req searchRequest) string {
	// Enforce line cap — single pass counts and caps simultaneously.
	var kept []string
	count := 0
	for _, l := range strings.Split(raw, "\n") {
		if l == "" {
			kept = append(kept, l)
			continue
		}
		count++
		if count <= req.maxMatches {
			kept = append(kept, l)
		}
	}
	truncated := count > req.maxMatches
	result := truncateOutput(strings.Join(kept, "\n"), 100)
	if truncated {
		result += "\n" + formatTruncatedHint(req.maxMatches, count, "'max_matches' or a more specific pattern")
	}
	if count == 0 {
		reason := e.classifyZeroMatch(req.pattern, req.searchPath, req.literal)
		result += "[no matches: " + reason + "]"
	}
	return result
}
