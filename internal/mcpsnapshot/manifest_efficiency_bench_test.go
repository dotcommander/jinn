package mcpsnapshot

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/voocel/mcp-sdk-go/protocol"
)

// BenchmarkManifestCanonicalization compares only the duplicate canonicalization
// and hash in HEAD Build with the single canonicalization in current Build.
// Tool encoding, sorting, lint, target fingerprint, and persistence are excluded.
func BenchmarkManifestCanonicalization(b *testing.B) {
	tools := make([]json.RawMessage, 100)
	for i := range tools {
		tool := &protocol.Tool{Name: fmt.Sprintf("tool_%03d", i), Description: "Local synthetic tool", InputSchema: protocol.JSONSchema{
			"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}},
		}}
		var err error
		tools[i], err = CanonicalJSON(tool)
		if err != nil {
			b.Fatal(err)
		}
	}
	manifest := snapshotManifestForLint(Snapshot{Server: json.RawMessage(`{"name":"local","version":"1"}`), Tools: tools})
	var result string
	for _, variant := range []struct {
		name string
		run  func() (string, error)
	}{
		{name: "HEAD_canonicalize_twice", run: func() (string, error) {
			fingerprint, err := Fingerprint(manifest)
			if err != nil {
				return "", err
			}
			_, err = CanonicalJSON(manifest)
			return fingerprint, err
		}},
		{name: "current_canonicalize_once", run: func() (string, error) {
			canonical, err := CanonicalJSON(manifest)
			if err != nil {
				return "", err
			}
			return fingerprintCanonical(canonical), nil
		}},
	} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				result, err = variant.run()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	if result == "" {
		b.Fatal("empty manifest fingerprint")
	}
}
