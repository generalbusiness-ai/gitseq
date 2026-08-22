// Package workroom implements the gitseq workroom application profile.
// It deliberately knows nothing about HTTP, MCP, or Git storage.
package workroom

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	// ProfileVersion identifies the exact deterministic application projection
	// contract. Kernel checkpoints are profile-independent verified event
	// material; application projection caches use this value as their gate.
	ProfileVersion     = "workroom-fold@7"
	SchemaStateLegacy  = "workroom/state@0"
	SchemaStateV1      = "workroom/state@1"
	SchemaState        = "workroom/state@2"
	SchemaRatifyLegacy = "workroom/ratify@0"
	SchemaRatify       = "workroom/ratify@1"
	SchemaSupersede    = "workroom/supersede@0"
)

type Kind string

const (
	KindAssert           Kind = "assert"
	KindPropose          Kind = "propose"
	KindRequest          Kind = "request"
	KindPromise          Kind = "promise"
	KindReport           Kind = "report"
	KindDissent          Kind = "dissent"
	KindArtifact         Kind = "artifact"
	KindRoster           Kind = "roster"
	KindInfraKey         Kind = "infra-key"
	KindSeal             Kind = "seal"
	KindAdmissionProfile Kind = "admission-profile"
)

// State is a durable attributed utterance. Body is intentionally restricted to
// string fields in v0: it is enough for stable identifiers, paths, keys, roles,
// conditions, dates, and canonical constraint expressions while keeping the
// representation tiny. Semantic validation belongs to the position-aware fold:
// unknown kinds and body keys survive decoding and remain visible there.
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
	return decode(schema, data, nil)
}

func decode(schema string, data []byte, pool map[string]string) (any, error) {
	var value any
	switch schema {
	case SchemaStateLegacy, SchemaStateV1, SchemaState:
		if pool == nil {
			value = &State{}
		} else {
			value = &pooledStatePayload{Text: pooledJSONText{pool: pool}}
		}
	case SchemaRatifyLegacy, SchemaRatify:
		value = &Ratify{}
	case SchemaSupersede:
		value = &Supersede{}
	default:
		return nil, fmt.Errorf("unsupported workroom schema %q", schema)
	}
	// The payload is already held in memory by the verified event. Unmarshal it
	// directly instead of constructing a Decoder, whose refill buffer copies
	// every payload and makes cold projection memory scale with all bytes read.
	// Unknown fields and alternate encodings still fail below: canonical
	// re-encoding cannot equal input that contains anything the typed value did
	// not consume. Unmarshal itself rejects trailing JSON values.
	if err := json.Unmarshal(data, value); err != nil {
		return nil, err
	}
	if pooled, ok := value.(*pooledStatePayload); ok {
		value = &State{Kind: pooled.Kind, Text: pooled.Text.value, Body: pooled.Body}
	}
	if err := validatePayload(value); err != nil {
		return nil, err
	}
	if !canonicalJSONEqual(value, data) {
		return nil, errors.New("workroom payload is not canonical JSON")
	}
	return value, nil
}

type pooledStatePayload struct {
	Kind Kind              `json:"kind"`
	Text pooledJSONText    `json:"text"`
	Body map[string]string `json:"body,omitempty"`
}

type pooledJSONText struct {
	pool  map[string]string
	value string
}

func (p *pooledJSONText) UnmarshalJSON(data []byte) error {
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
		content := data[1 : len(data)-1]
		unescaped := true
		for _, value := range content {
			if value == '\\' {
				unescaped = false
				break
			}
		}
		if unescaped {
			if existing, ok := p.pool[string(content)]; ok {
				p.value = existing
			} else {
				p.value = string(content)
			}
			return nil
		}
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if existing, ok := p.pool[value]; ok {
		p.value = existing
	} else {
		p.value = value
	}
	return nil
}

// canonicalJSONEqual compares encoding/json's canonical struct and map output
// directly with the verified payload. Encoder writes one trailing newline;
// accepting exactly that extra byte avoids Marshal's full-size output copy
// while preserving the existing byte-for-byte canonicality requirement.
func canonicalJSONEqual(value any, data []byte) bool {
	comparison := canonicalJSONComparison{want: data, equal: true}
	if err := json.NewEncoder(&comparison).Encode(value); err != nil {
		return false
	}
	return comparison.equal && comparison.offset == len(data)+1
}

type canonicalJSONComparison struct {
	want   []byte
	offset int
	equal  bool
}

func (c *canonicalJSONComparison) Write(content []byte) (int, error) {
	for _, value := range content {
		switch {
		case c.offset < len(c.want):
			c.equal = c.equal && value == c.want[c.offset]
		case c.offset == len(c.want):
			c.equal = c.equal && value == '\n'
		default:
			c.equal = false
		}
		c.offset++
	}
	return len(content), nil
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
