// Package mcpsnapshot owns canonical MCP tool manifests and approval snapshots.
package mcpsnapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

// CanonicalJSON encodes a JSON value with lexical object-key order and no HTML escaping.
func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("canonical JSON must contain exactly one value")
	}
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, decoded); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch item := value.(type) {
	case map[string]any:
		return writeCanonicalJSONObject(output, item)
	case []any:
		return writeCanonicalJSONArray(output, item)
	default:
		encoded, err := encodeJSON(value)
		if err != nil {
			return err
		}
		output.Write(encoded)
	}
	return nil
}

func writeCanonicalJSONObject(output *bytes.Buffer, object map[string]any) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output.WriteByte('{')
	for index, key := range keys {
		if index != 0 {
			output.WriteByte(',')
		}
		encodedKey, err := encodeJSON(key)
		if err != nil {
			return err
		}
		output.Write(encodedKey)
		output.WriteByte(':')
		if err := writeCanonicalJSON(output, object[key]); err != nil {
			return err
		}
	}
	output.WriteByte('}')
	return nil
}

func writeCanonicalJSONArray(output *bytes.Buffer, array []any) error {
	output.WriteByte('[')
	for index, member := range array {
		if index != 0 {
			output.WriteByte(',')
		}
		if err := writeCanonicalJSON(output, member); err != nil {
			return err
		}
	}
	output.WriteByte(']')
	return nil
}

func encodeJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

// Fingerprint returns the documented sha256:<hex> identity for canonical data.
func Fingerprint(value any) (string, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
