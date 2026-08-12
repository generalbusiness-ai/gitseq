package perflane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// CanonicalJSON normalizes a JSON-compatible value to compact JSON with
// deterministic object-key ordering. Numbers retain their JSON spelling.
func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical JSON input: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, fmt.Errorf("decode canonical JSON input: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical JSON: %w", err)
	}
	return canonical, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("canonical JSON input contains more than one value")
		}
		return fmt.Errorf("decode canonical JSON input: %w", err)
	}
	return nil
}

// CorrectnessDigest returns a stable lowercase SHA-256 digest for a
// JSON-compatible result.
func CorrectnessDigest(value any) (string, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
