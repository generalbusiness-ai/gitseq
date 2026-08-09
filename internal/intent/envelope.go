package intent

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

const eventMarker = "gitseq-event-v0"

func Envelope(s Signed, restsOn []string) string {
	var b strings.Builder
	b.WriteString(eventMarker)
	b.WriteString("\nIntent: ")
	b.WriteString(B64(s.Intent))
	b.WriteString("\nActor-Key: ")
	b.WriteString(B64(s.ActorKey))
	b.WriteString("\nActor-Signature: ")
	b.WriteString(B64(s.Signature))
	for _, ref := range restsOn {
		b.WriteString("\nRests-On: ")
		b.WriteString(ref)
	}
	b.WriteByte('\n')
	return b.String()
}

func ParseEnvelope(message string, maxBytes uint64) (Signed, []string, error) {
	if maxBytes == 0 || uint64(len(message)) > maxBytes {
		return Signed{}, nil, fmt.Errorf("event envelope exceeds %d bytes", maxBytes)
	}
	lines := strings.Split(message, "\n")
	if len(lines) == 0 || lines[0] != eventMarker {
		return Signed{}, nil, errors.New("not a gitseq event envelope")
	}
	values := map[string]string{}
	var rests []string
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			return Signed{}, nil, fmt.Errorf("malformed envelope line %q", line)
		}
		if key == "Rests-On" {
			rests = append(rests, value)
			continue
		}
		if _, exists := values[key]; exists {
			return Signed{}, nil, fmt.Errorf("duplicate envelope field %s", key)
		}
		values[key] = value
	}
	if len(values) != 3 {
		return Signed{}, nil, errors.New("event envelope requires exactly three signed fields")
	}
	encoded, err := UnB64(values["Intent"])
	if err != nil {
		return Signed{}, nil, err
	}
	actor, err := UnB64(values["Actor-Key"])
	if err != nil {
		return Signed{}, nil, err
	}
	sig, err := UnB64(values["Actor-Signature"])
	if err != nil {
		return Signed{}, nil, err
	}
	return Signed{Intent: encoded, ActorKey: actor, Signature: sig}, rests, nil
}

func EqualRefs(a, b []string) bool {
	return bytes.Equal([]byte(strings.Join(a, "\x00")), []byte(strings.Join(b, "\x00")))
}
