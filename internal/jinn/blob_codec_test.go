package jinn

import (
	"bytes"
	"testing"
)

func TestBlobCodec_Roundtrip_Raw(t *testing.T) {
	t.Parallel()
	original := []byte("hello world")
	encoded, err := encodeBlob(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBlob(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("roundtrip failed: got %q, want %q", decoded, original)
	}
	if encoded[0] != blobTagRaw {
		t.Error("small payload should use raw tag")
	}
}

func TestBlobCodec_Roundtrip_Compressed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	for i := 0; i < 1000; i++ {
		buf.WriteString("this is a repeating line of text\n")
	}
	original := buf.Bytes()
	encoded, err := encodeBlob(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBlob(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("roundtrip failed: decoded length %d, want %d", len(decoded), len(original))
	}
	if encoded[0] != blobTagGzip {
		t.Error("compressible payload should use gzip tag")
	}
	if len(encoded) >= len(original) {
		t.Errorf("compressed size %d >= original %d", len(encoded), len(original))
	}
}

func TestBlobCodec_EmptyInput(t *testing.T) {
	t.Parallel()
	encoded, err := encodeBlob(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 1 {
		t.Errorf("empty input should encode to 1 byte (tag), got %d", len(encoded))
	}
	decoded, err := decodeBlob(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 0 {
		t.Errorf("empty input roundtrip: got %d bytes, want 0", len(decoded))
	}
}

func TestBlobCodec_NilDecode(t *testing.T) {
	t.Parallel()
	decoded, err := decodeBlob(nil)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != nil {
		t.Errorf("nil decode: got %v, want nil", decoded)
	}
}

func TestBlobCodec_IncompressibleFallback(t *testing.T) {
	t.Parallel()
	original := make([]byte, 256)
	for i := range original {
		original[i] = byte(i)
	}
	encoded, err := encodeBlob(original)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != blobTagRaw {
		t.Error("incompressible data should fall back to raw tag")
	}
	decoded, err := decodeBlob(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, original) {
		t.Error("roundtrip failed for incompressible data")
	}
}

func TestBlobCodec_UnknownTag(t *testing.T) {
	t.Parallel()
	_, err := decodeBlob([]byte{0xFF, 0x00})
	if err == nil {
		t.Error("expected error for unknown tag")
	}
}
