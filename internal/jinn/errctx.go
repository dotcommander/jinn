package jinn

// ErrWithSuggestion wraps an error with a one-sentence next-step suggestion
// for the calling LLM. Suggestions follow the style guide below so future
// maintainers add new suggestions consistently.
//
// Style guide for suggestion strings:
//   - Imperative mood ("use list_dir", "verify the path", "check ownership")
//   - One sentence only — no hedging words ("might", "perhaps", "try to")
//   - Name the specific tool or parameter the agent should use next
//   - No trailing period
type ErrWithSuggestion struct {
	Err        error
	Suggestion string
}

func (e *ErrWithSuggestion) Error() string { return e.Err.Error() }
func (e *ErrWithSuggestion) Unwrap() error { return e.Err }
