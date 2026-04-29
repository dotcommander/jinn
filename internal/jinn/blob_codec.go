// Package jinn provides a blob codec for compressing undo history snapshots.
//
// Adapted from: https://github.com/frane/agented@9f88dae/internal/store/blob/blob.go
// What changed: extracted as standalone codec with no dependencies on agented internals;
//
//	kept the gzip+raw adaptive strategy with tag-prefix encoding;
//	added fsync-free API (callers handle durability).
//
// Date: 2026-04-29
//
// The pattern: blob storage with adaptive compression. Small payloads are stored
// raw (compression overhead exceeds savings). Larger payloads get gzip. A 1-byte
// tag prefix makes decoding unambiguous. If gzip makes it larger (already-
// compressed data), fall back to raw. This gives good compression for text edits
// while avoiding the double-compression penalty on binary content.
package jinn

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

const (
	blobTagRaw  = byte(0x00)
	blobTagGzip = byte(0x01)

	// blobRawThreshold: payloads smaller than this are stored raw.
	// Single-line edits dominate by count and are usually well under 64 bytes.
	blobRawThreshold = 64

	// blobDecodeMax caps decompressed gzip output. Encode side is gated at
	// historyMaxBlobBytes (5 MiB); allow 2x for any future expansion ratio.
	blobDecodeMax = historyMaxBlobBytes * 2
)

// rawBlob returns plain bytes with the raw tag prefix.
func rawBlob(plain []byte) []byte {
	out := make([]byte, 0, 1+len(plain))
	out = append(out, blobTagRaw)
	return append(out, plain...)
}

// encodeBlob wraps plain bytes with a 1-byte tag and either gzip output or
// raw bytes. Empty input encodes to a single tag byte. Falls back to raw if
// gzip would not shrink the payload.
func encodeBlob(plain []byte) ([]byte, error) {
	if len(plain) < blobRawThreshold {
		return rawBlob(plain), nil
	}
	var buf bytes.Buffer
	buf.WriteByte(blobTagGzip)
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(plain); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	if buf.Len()-1 >= len(plain) {
		return rawBlob(plain), nil
	}
	return buf.Bytes(), nil
}

// decodeBlob reverses encodeBlob. Empty input returns nil bytes (supports
// "no blob recorded"). Decompressed output is capped at blobDecodeMax.
func decodeBlob(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	switch b[0] {
	case blobTagRaw:
		if len(b) == 1 {
			return nil, nil
		}
		out := make([]byte, len(b)-1)
		copy(out, b[1:])
		return out, nil
	case blobTagGzip:
		gr, err := gzip.NewReader(bytes.NewReader(b[1:]))
		if err != nil {
			return nil, fmt.Errorf("blob: gzip header: %w", err)
		}
		defer gr.Close()
		out, err := io.ReadAll(io.LimitReader(gr, blobDecodeMax+1))
		if err != nil {
			return nil, fmt.Errorf("blob: gzip body: %w", err)
		}
		if len(out) > blobDecodeMax {
			return nil, fmt.Errorf("blob: decompressed size exceeds %d bytes", blobDecodeMax)
		}
		return out, nil
	default:
		return nil, errors.New("blob: unknown codec tag")
	}
}
