package mcpbatch

import (
	"fmt"
	"testing"

	"github.com/voocel/mcp-sdk-go/protocol"
)

// BenchmarkValidateRepeatedToolSchema compares the HEAD compile-per-call path
// with the per-preflight schema reuse path. It excludes manifest lookup and
// execution; both variants validate the same 20 argument maps each iteration.
func BenchmarkValidateRepeatedToolSchema(b *testing.B) {
	tool := &protocol.Tool{Name: "read", InputSchema: protocol.JSONSchema{
		"type": "object", "properties": map[string]any{
			"count": map[string]any{"type": "integer", "minimum": 0},
		}, "required": []string{"count"}, "additionalProperties": false,
	}}
	arguments := make([]map[string]any, 20)
	for i := range arguments {
		arguments[i] = map[string]any{"count": i}
	}
	for _, variant := range []struct {
		name string
		run  func() error
	}{
		{name: "HEAD_compile_each_call", run: func() error {
			for _, args := range arguments {
				schema, err := compileApprovedSchema(tool)
				if err != nil {
					return err
				}
				if err := schema.Validate(args); err != nil {
					return err
				}
			}
			return nil
		}},
		{name: "current_compile_once_per_batch", run: func() error {
			schema, err := compileApprovedSchema(tool)
			if err != nil {
				return err
			}
			for _, args := range arguments {
				if err := schema.Validate(args); err != nil {
					return err
				}
			}
			return nil
		}},
	} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := variant.run(); err != nil {
					b.Fatal(fmt.Errorf("20-call schema validation: %w", err))
				}
			}
		})
	}
}
