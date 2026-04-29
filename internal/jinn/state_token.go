// Package jinn provides state-token helpers for optimistic concurrency control
// over file edits.
//
// Adapted from: https://github.com/frane/agented@9f88dae/internal/store/types.go
// What changed: extracted ComputeStateToken + HashContent as standalone functions;
//   replaced agented-specific format string with jinn-specific namespace;
//   removed all SQLite/edit-tree domain types.
// Date: 2026-04-29
//
// The pattern: every state of a file gets a deterministic fingerprint. Reads
// return it. Writes accept an optional "expect" token. If the file changed
// between read and write (by another process, another agent, or a concurrent
// tool call), the token won't match and the write rejects with the current
// content attached. One round trip on conflict, no "re-read before every write"
// ritual, no defensive re-reads.
package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// StateTokenLen is the number of hex characters in a state token.
const StateTokenLen = 16

// HashContent returns the hex SHA-256 of the given content.
// This is the building block for state tokens — a content hash that
// deterministically identifies a file's current state.
func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// StateToken computes a deterministic state token for a file.
// The token encodes (path, mtime_ns, content_hash) so that:
//   - Same content + same mtime = same token (idempotent reads)
//   - Content change = different token (detect concurrent edits)
//   - Path change = different token (files are distinct)
//
// Returns the first StateTokenLen hex characters of SHA-256.
func StateToken(absPath string, mtimeNs int64, contentHash string) string {
	input := fmt.Sprintf("jinn:v1:%s:%d:%s", absPath, mtimeNs, contentHash)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:StateTokenLen]
}
