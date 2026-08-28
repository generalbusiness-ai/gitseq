package intent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

const (
	Version             uint64 = 0
	domainTag                  = "gitseq.intent.v0\x00"
	MaxStringBytes             = 32 << 10
	MaxCausalReferences        = 4096
)

var (
	enc cbor.EncMode
	dec cbor.DecMode
)

func init() {
	var err error
	enc, err = cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	dec, err = (cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		IndefLength:      cbor.IndefLengthForbidden,
		TagsMd:           cbor.TagsForbidden,
		MaxNestedLevels:  8,
		MaxArrayElements: 4096,
		MaxMapPairs:      16,
	}).DecMode()
	if err != nil {
		panic(err)
	}
}

// Intent is encoded as a fixed CBOR array. The leading anonymous field makes
// fxamacker/cbor use array representation; the wire has no maps, floats, tags,
// or indefinite-length values.
type Intent struct {
	_               struct{} `cbor:",toarray"`
	Version         uint64
	Target          string
	Schema          string
	EnvelopeVersion uint64
	PayloadTree     string
	RestsOn         []string
	IdempotencyNS   string
	IdempotencyKey  string
	CapabilityHash  []byte
}

type Signed struct {
	Intent    []byte `json:"intent"`
	ActorKey  []byte `json:"actor_key"`
	Signature []byte `json:"signature"`
}

func (i Intent) Validate() error {
	if i.Version != Version {
		return fmt.Errorf("unsupported intent version %d", i.Version)
	}
	for name, value := range map[string]string{
		"target": i.Target, "schema": i.Schema, "payload tree": i.PayloadTree,
		"idempotency namespace": i.IdempotencyNS, "idempotency key": i.IdempotencyKey,
	} {
		if value == "" {
			return fmt.Errorf("%s is required", name)
		}
		if len(value) > MaxStringBytes {
			return fmt.Errorf("%s exceeds %d bytes", name, MaxStringBytes)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s contains a forbidden control byte", name)
		}
	}
	if len(i.RestsOn) > MaxCausalReferences {
		return fmt.Errorf("causal references exceed %d entries", MaxCausalReferences)
	}
	for _, ref := range i.RestsOn {
		if ref == "" || len(ref) > MaxStringBytes || strings.ContainsAny(ref, "\r\n\x00") {
			return errors.New("invalid causal reference")
		}
	}
	return nil
}

func Encode(i Intent) ([]byte, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return enc.Marshal(i)
}

func Decode(data []byte) (Intent, error) {
	var i Intent
	if err := dec.Unmarshal(data, &i); err != nil {
		return Intent{}, fmt.Errorf("decode intent: %w", err)
	}
	if err := i.Validate(); err != nil {
		return Intent{}, err
	}
	canonical, err := enc.Marshal(i)
	if err != nil {
		return Intent{}, err
	}
	if !bytes.Equal(canonical, data) {
		return Intent{}, errors.New("intent is not core-deterministic CBOR")
	}
	return i, nil
}

// SigningBytes returns a fresh copy of the domain-separated bytes covered by
// an actor signature. Only canonical encoded intents have signing bytes.
func SigningBytes(encoded []byte) ([]byte, error) {
	if _, err := Decode(encoded); err != nil {
		return nil, err
	}
	message := make([]byte, 0, len(domainTag)+len(encoded))
	message = append(message, domainTag...)
	message = append(message, encoded...)
	return message, nil
}

func Sign(i Intent, private ed25519.PrivateKey) (Signed, error) {
	encoded, err := Encode(i)
	if err != nil {
		return Signed{}, err
	}
	message, err := SigningBytes(encoded)
	if err != nil {
		return Signed{}, err
	}
	return Signed{
		Intent:    encoded,
		ActorKey:  append([]byte(nil), private.Public().(ed25519.PublicKey)...),
		Signature: ed25519.Sign(private, message),
	}, nil
}

func Verify(s Signed) (Intent, error) {
	if len(s.ActorKey) != ed25519.PublicKeySize {
		return Intent{}, errors.New("invalid actor public key length")
	}
	if len(s.Signature) != ed25519.SignatureSize {
		return Intent{}, errors.New("invalid actor signature length")
	}
	i, err := Decode(s.Intent)
	if err != nil {
		return Intent{}, err
	}
	message, err := SigningBytes(s.Intent)
	if err != nil {
		return Intent{}, err
	}
	if !ed25519.Verify(ed25519.PublicKey(s.ActorKey), message, s.Signature) {
		return Intent{}, errors.New("invalid actor signature")
	}
	return i, nil
}

func (s Signed) DedupKey() (string, error) {
	i, err := Verify(s)
	if err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(s.ActorKey)
	return i.Target + "\x00" + hex.EncodeToString(fingerprint[:]) + "\x00" + i.IdempotencyNS + "\x00" + i.IdempotencyKey, nil
}

func (s Signed) Equal(other Signed) bool {
	return bytes.Equal(s.Intent, other.Intent) && bytes.Equal(s.ActorKey, other.ActorKey) && bytes.Equal(s.Signature, other.Signature)
}

func B64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func UnB64(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func ActorFingerprint(key []byte) string {
	digest := sha256.Sum256(key)
	return hex.EncodeToString(digest[:])
}
