// Package workroom implements the gitseq workroom application profile.
// It deliberately knows nothing about HTTP, MCP, or Git storage.
package workroom

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	// ProfileVersion binds resident checkpoints to the exact deterministic
	// fold contract that produced them. Any semantic fold change must advance
	// this value before old checkpoints may be reused.
	ProfileVersion  = "workroom-fold@1"
	SchemaState     = "workroom/state@0"
	SchemaRatify    = "workroom/ratify@0"
	SchemaSupersede = "workroom/supersede@0"
)

type Kind string

const (
	KindAssert   Kind = "assert"
	KindPropose  Kind = "propose"
	KindRequest  Kind = "request"
	KindPromise  Kind = "promise"
	KindReport   Kind = "report"
	KindDissent  Kind = "dissent"
	KindArtifact Kind = "artifact"
	KindRoster   Kind = "roster"
	KindInfraKey Kind = "infra-key"
	KindSeal     Kind = "seal"
)

var knownKinds = map[Kind]bool{
	KindAssert: true, KindPropose: true, KindRequest: true,
	KindPromise: true, KindReport: true, KindDissent: true,
	KindArtifact: true, KindRoster: true, KindInfraKey: true,
	KindSeal: true,
}

// State is a durable attributed utterance. Body is intentionally restricted to
// string fields in v0: it is enough for stable identifiers, paths, keys, roles,
// conditions, and dates while keeping the canonical representation tiny.
// Unknown kinds and body keys survive decoding and project as opaque records.
type State struct {
	Kind Kind              `json:"kind"`
	Text string            `json:"text"`
	Body map[string]string `json:"body,omitempty"`
}

type Ratify struct {
	Target string `json:"target"`
}

type Supersede struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

func Encode(value any) ([]byte, error) {
	if err := validatePayload(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func Decode(schema string, data []byte) (any, error) {
	var value any
	switch schema {
	case SchemaState:
		value = &State{}
	case SchemaRatify:
		value = &Ratify{}
	case SchemaSupersede:
		value = &Supersede{}
	default:
		return nil, fmt.Errorf("unsupported workroom schema %q", schema)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("multiple JSON values")
	}
	if err := validatePayload(value); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data) {
		return nil, errors.New("workroom payload is not canonical JSON")
	}
	return value, nil
}

func validatePayload(value any) error {
	switch body := value.(type) {
	case State:
		return validateState(body)
	case *State:
		return validateState(*body)
	case Ratify:
		if body.Target == "" {
			return errors.New("ratify target is required")
		}
	case *Ratify:
		return validatePayload(*body)
	case Supersede:
		if body.Target == "" || body.Text == "" {
			return errors.New("supersede target and text are required")
		}
	case *Supersede:
		return validatePayload(*body)
	default:
		return fmt.Errorf("unsupported workroom payload %T", value)
	}
	return nil
}

func validateState(state State) error {
	if state.Kind == "" || state.Text == "" {
		return errors.New("state kind and text are required")
	}
	for key, value := range state.Body {
		if key == "" || value == "" {
			return errors.New("state body keys and values must be non-empty")
		}
	}
	if !knownKinds[state.Kind] {
		return nil
	}
	required := map[Kind][]string{
		KindRequest:  {"to", "conditions"},
		KindRoster:   {"actor", "name", "role"},
		KindInfraKey: {"service", "public_key"},
		KindArtifact: {"path", "commit"},
	}
	for _, field := range required[state.Kind] {
		if state.Body[field] == "" {
			return fmt.Errorf("%s state requires body.%s", state.Kind, field)
		}
	}
	if state.Kind == KindRoster && state.Body["role"] == "participant" && state.Body["kind"] == "" {
		return errors.New("participant roster state requires body.kind")
	}
	return nil
}

func SortedBodyKeys(body map[string]string) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
