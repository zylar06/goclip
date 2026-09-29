package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// decodeJSON accepts a single JSON value, optionally in one Markdown fence.
// It rejects duplicate keys, trailing values, excessive nesting and unknown
// schema fields rather than recovering arbitrary substrings from server output.
func decodeJSON(raw string, dst any) error {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```json\n") || strings.HasPrefix(raw, "```\n") {
		if !strings.HasSuffix(raw, "\n```") {
			return invalid("Model returned an incomplete JSON fence.")
		}
		raw = raw[strings.IndexByte(raw, '\n')+1 : len(raw)-4]
	}
	if len(raw) == 0 || len(raw) > maxResponseBytes || !utf8.ValidString(raw) {
		return invalid("Expected bounded UTF-8 stage JSON.")
	}
	if err := checkJSON([]byte(raw)); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("Stage JSON does not match the requested schema; no automatic repair request was made.")
	}
	return nil
}

func checkJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := checkValue(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid("Stage JSON must contain exactly one value.")
	}
	return nil
}

func checkValue(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return invalid("Stage JSON nesting is too deep.")
	}
	token, err := dec.Token()
	if err != nil {
		return invalid("Model returned malformed or incomplete JSON.")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return invalid("Malformed JSON object.")
			}
			key, ok := keyToken.(string)
			canonical := strings.ToLower(key) // encoding/json struct fields are case-insensitive.
			if !ok || seen[canonical] {
				return invalid("Stage JSON contains duplicate or invalid object keys.")
			}
			seen[canonical] = true
			if err := checkValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := checkValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return invalid("Unexpected JSON delimiter.")
	}
	end, err := dec.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return invalid("Incomplete JSON value.")
	}
	return nil
}
