// Package mcpbatch owns strict, snapshot-gated MCP batch input validation and execution.
package mcpbatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const maxInputBytes = 1 << 20

// Input is the versioned, bounded batch request document.
type Input struct {
	Version        int           `json:"version"`
	MaxConcurrency *int          `json:"max_concurrency,omitempty"`
	FailFast       bool          `json:"fail_fast,omitempty"`
	Calls          []Call        `json:"calls"`
	CallTimeout    time.Duration `json:"-"`
}

// Call is one named, schema-validated read-only tool invocation.
type Call struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Select    *string        `json:"select,omitempty"`
	Head      *int           `json:"head,omitempty"`
}

// Decode reads one strict JSON object without accepting more than 1 MiB.
func Decode(reader io.Reader) (Input, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil {
		return Input{}, fmt.Errorf("read batch input: %w", err)
	}
	if len(data) > maxInputBytes {
		return Input{}, errors.New("batch input exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var input Input
	if err := decoder.Decode(&input); err != nil {
		return Input{}, fmt.Errorf("invalid batch input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Input{}, errors.New("batch input must contain exactly one JSON object")
	}
	if err := validateInput(input); err != nil {
		return Input{}, err
	}
	if input.MaxConcurrency == nil {
		defaultConcurrency := 2
		input.MaxConcurrency = &defaultConcurrency
	}
	if input.CallTimeout <= 0 {
		input.CallTimeout = 30 * time.Second
	}
	return input, nil
}

func validateInput(input Input) error {
	if input.Version != 1 {
		return fmt.Errorf("unsupported batch version %d", input.Version)
	}
	if len(input.Calls) == 0 || len(input.Calls) > 20 {
		return errors.New("batch calls must contain between 1 and 20 entries")
	}
	if input.MaxConcurrency != nil && (*input.MaxConcurrency < 1 || *input.MaxConcurrency > 8) {
		return errors.New("batch max_concurrency must be between 1 and 8")
	}
	ids := make(map[string]struct{}, len(input.Calls))
	for index, call := range input.Calls {
		if call.ID == "" {
			return fmt.Errorf("batch call at index %d has an empty id", index)
		}
		if _, exists := ids[call.ID]; exists {
			return fmt.Errorf("batch call id %q is duplicated", call.ID)
		}
		ids[call.ID] = struct{}{}
		if err := validateCall(call); err != nil {
			return err
		}
	}
	return nil
}

func validateCall(call Call) error {
	if call.Tool == "" {
		return fmt.Errorf("batch call %q has an empty tool", call.ID)
	}
	if call.Head != nil && *call.Head < 0 {
		return fmt.Errorf("batch call %q head must be a non-negative integer", call.ID)
	}
	if call.Head != nil && call.Select == nil {
		return fmt.Errorf("batch call %q head requires select", call.ID)
	}
	if call.Select != nil {
		if err := validatePointer(*call.Select); err != nil {
			return fmt.Errorf("batch call %q: %w", call.ID, err)
		}
	}
	return nil
}

func validatePointer(pointer string) error {
	if pointer == "" {
		return nil
	}
	if pointer[0] != '/' {
		return errors.New("select must be an RFC 6901 JSON Pointer")
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 == len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1') {
			return errors.New("select must use ~0 for '~' and ~1 for '/'")
		}
		index++
	}
	return nil
}
