package jinn

import (
	"fmt"
	"os"
	"strings"
)

func (e *Engine) diffFiles(args map[string]interface{}) (*ToolResult, error) {
	pathA, _ := args["path_a"].(string)
	pathB, _ := args["path_b"].(string)
	contextLines := intArg(args, "context_lines", 3)

	if pathA == "" || pathB == "" {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("diff_files requires both path_a and path_b"),
			Suggestion: "provide two file paths to compare",
			Code:       ErrCodeInvalidArgs,
		}
	}

	resolvedA, err := e.checkPath(pathA)
	if err != nil {
		return nil, err
	}
	resolvedB, err := e.checkPath(pathB)
	if err != nil {
		return nil, err
	}

	contentA, err := os.ReadFile(resolvedA)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", pathA),
				Suggestion: "check the file path",
				Code:       ErrCodeFileNotFound,
			}
		}
		return nil, err
	}
	contentB, err := os.ReadFile(resolvedB)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", pathB),
				Suggestion: "check the file path",
				Code:       ErrCodeFileNotFound,
			}
		}
		return nil, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", pathA, pathB)
	ok, firstLine := renderDiffBody(string(contentA), string(contentB), contextLines, &b)

	if !ok {
		return &ToolResult{
			Text: "files are identical",
			Meta: map[string]any{
				"is_identical":      true,
				"first_changed_line": 0,
			},
		}, nil
	}

	return &ToolResult{
		Text: strings.TrimRight(b.String(), "\n"),
		Meta: map[string]any{
			"is_identical":      false,
			"first_changed_line": firstLine,
		},
	}, nil
}
