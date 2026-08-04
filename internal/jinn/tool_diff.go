package jinn

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

const (
	diffMaxFileBytes    = 10 << 20
	diffMaxLines        = 100000
	diffOperationBudget = 10_000_000
)

func (e *Engine) diffFiles(args map[string]interface{}) (*ToolResult, error) {
	pathA, _ := args["path_a"].(string)
	pathB, _ := args["path_b"].(string)
	contextLines := intArg(args, "context_lines", 3)

	if pathA == "" || pathB == "" {
		return nil, &ErrWithSuggestion{
			Err:        errors.New("diff_files requires both path_a and path_b"),
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

	contentA, _, err := e.rootedReadFile(resolvedA, diffMaxFileBytes)
	if err != nil {
		return nil, err
	}
	contentB, _, err := e.rootedReadFile(resolvedB, diffMaxFileBytes)
	if err != nil {
		return nil, err
	}
	linesA := bytes.Count(contentA, []byte{'\n'}) + 1
	linesB := bytes.Count(contentB, []byte{'\n'}) + 1
	if linesA+linesB > diffMaxLines {
		return nil, &ErrWithSuggestion{Err: fmt.Errorf("diff_files resource limit: %d combined lines exceed %d", linesA+linesB, diffMaxLines), Suggestion: "compare smaller files or use search_files to narrow the changed region", Code: ErrCodeResourceLimit}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", pathA, pathB)
	script, err := linearSpaceEditScript(string(contentA), string(contentB), diffOperationBudget)
	if err != nil {
		return nil, &ErrWithSuggestion{Err: err, Suggestion: "compare smaller files or use search_files to narrow the changed region", Code: ErrCodeResourceLimit}
	}
	hunks := computeHunks(script, contextLines)
	ok := len(hunks) > 0
	firstLine := 0
	if ok {
		firstLine = formatHunks(script, hunks, contextLines, &b)
	}

	if !ok {
		return &ToolResult{
			Text: "files are identical",
			Meta: map[string]any{
				"is_identical":       true,
				"first_changed_line": 0,
			},
		}, nil
	}

	return &ToolResult{
		Text: strings.TrimRight(b.String(), "\n"),
		Meta: map[string]any{
			"is_identical":       false,
			"first_changed_line": firstLine,
		},
	}, nil
}

type diffBudget struct {
	remaining int
}

func (b *diffBudget) spend(amount int) error {
	b.remaining -= amount
	if b.remaining < 0 {
		return errors.New("diff_files resource limit: operation budget exhausted")
	}
	return nil
}

// linearSpaceEditScript uses Hirschberg's divide-and-conquer LCS construction.
// It has linear working memory and a hard cell-operation budget, avoiding the
// quadratic full matrix previously used by diff_files.
func linearSpaceEditScript(old, newText string, operationBudget int) ([]diffOp, error) {
	left := splitDiffLines(old)
	right := splitDiffLines(newText)
	budget := &diffBudget{remaining: operationBudget}
	return hirschbergScript(left, right, budget)
}

//nolint:funlen,gocognit,gocyclo,revive // Hirschberg's base/partition cases must remain adjacent to preserve the operation budget.
func hirschbergScript(left, right []string, budget *diffBudget) ([]diffOp, error) {
	switch {
	case len(left) == 0:
		out := make([]diffOp, len(right))
		for i, line := range right {
			out[i] = diffOp{tag: '+', value: line}
		}
		return out, nil
	case len(right) == 0:
		out := make([]diffOp, len(left))
		for i, line := range left {
			out[i] = diffOp{tag: '-', value: line}
		}
		return out, nil
	case len(left) == 1:
		if err := budget.spend(len(right)); err != nil {
			return nil, err
		}
		match := -1
		for i, line := range right {
			if line == left[0] {
				match = i
				break
			}
		}
		if match < 0 {
			out := []diffOp{{tag: '-', value: left[0]}}
			for _, line := range right {
				out = append(out, diffOp{tag: '+', value: line})
			}
			return out, nil
		}
		out := make([]diffOp, 0, len(right))
		for _, line := range right[:match] {
			out = append(out, diffOp{tag: '+', value: line})
		}
		out = append(out, diffOp{tag: ' ', value: left[0]})
		for _, line := range right[match+1:] {
			out = append(out, diffOp{tag: '+', value: line})
		}
		return out, nil
	}

	middle := len(left) / 2
	forward, err := lcsLengths(left[:middle], right, false, budget)
	if err != nil {
		return nil, err
	}
	backward, err := lcsLengths(left[middle:], right, true, budget)
	if err != nil {
		return nil, err
	}
	split := 0
	best := -1
	for index := 0; index <= len(right); index++ {
		score := forward[index] + backward[len(right)-index]
		if score > best {
			best = score
			split = index
		}
	}
	first, err := hirschbergScript(left[:middle], right[:split], budget)
	if err != nil {
		return nil, err
	}
	second, err := hirschbergScript(left[middle:], right[split:], budget)
	if err != nil {
		return nil, err
	}
	return append(first, second...), nil
}

func lcsLengths(left, right []string, reverse bool, budget *diffBudget) ([]int, error) {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for i := range left {
		if err := budget.spend(len(right)); err != nil {
			return nil, err
		}
		for j := range right {
			li, rj := i, j
			if reverse {
				li = len(left) - 1 - i
				rj = len(right) - 1 - j
			}
			if left[li] == right[rj] {
				current[j+1] = previous[j] + 1
			} else {
				current[j+1] = max(current[j], previous[j+1])
			}
		}
		previous, current = current, previous
		clear(current)
	}
	return previous, nil
}
