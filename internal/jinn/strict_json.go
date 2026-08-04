package jinn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeOneRequest accepts exactly one bounded JSON object and rejects
// duplicate keys before ordinary struct decoding can collapse them.
func DecodeOneRequest(reader io.Reader, maxBytes int64) (Request, error) {
	var req Request
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return req, fmt.Errorf("read request: %w", err)
	}
	if len(data) == 0 {
		return req, io.EOF
	}
	if int64(len(data)) > maxBytes {
		return req, fmt.Errorf("request exceeds %d bytes", maxBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return req, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	if err := requireJSONEOF(dec); err != nil {
		return req, err
	}
	return req, nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := scanJSONValue(dec, "$", true); err != nil {
		return err
	}
	return requireJSONEOF(dec)
}

func requireJSONEOF(dec *json.Decoder) error {
	if token, err := dec.Token(); err == nil {
		return fmt.Errorf("trailing JSON value or token %v", token)
	} else if err != io.EOF {
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

//nolint:gocognit,gocyclo,revive // recursive JSON shape validation keeps duplicate-key reporting at the exact path.
func scanJSONValue(dec *json.Decoder, path string, requireObject bool) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if requireObject && (!isDelim || delim != '{') {
		return errors.New("request must be a JSON object")
	}
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			keyToken, tokenErr := dec.Token()
			if tokenErr != nil {
				return tokenErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s contains a non-string object key", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if valueErr := scanJSONValue(dec, path+"."+key, false); valueErr != nil {
				return valueErr
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		index := 0
		for dec.More() {
			if valueErr := scanJSONValue(dec, fmt.Sprintf("%s[%d]", path, index), false); valueErr != nil {
				return valueErr
			}
			index++
		}
		_, err = dec.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}
