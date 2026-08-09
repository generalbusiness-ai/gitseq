package workroom

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const GenesisAdmissionContract = "gitseq-genesis-v0"

type AdmissionProfile struct {
	Event     string `json:"event,omitempty"`
	Bundle    string `json:"bundle"`
	Contract  string `json:"contract"`
	Genesis   string `json:"genesis"`
	Bootstrap bool   `json:"bootstrap,omitempty"`
}

type AdmissionProfileStatus string

const (
	AdmissionProfileAvailable   AdmissionProfileStatus = "available"
	AdmissionProfileUnavailable AdmissionProfileStatus = "profile-unavailable"
)

type AdmissionProfileResolution struct {
	Profile AdmissionProfile       `json:"profile"`
	Status  AdmissionProfileStatus `json:"status"`
}

// SelectAdmissionProfile resolves the profile in force for the supplied log
// prefix. To select at an arbitrary sequence base, callers pass only records
// at or before that base. Retirement deselects an activation; staleness does
// not. When no activation is in force, the deterministic bootstrap profile is
// selected.
func SelectAdmissionProfile(records []Record, genesis string) (AdmissionProfile, error) {
	return NewFolder(records).AdmissionProfile(genesis)
}

// AdmissionProfile resolves the profile in force at the Folder's current
// prefix without mutating fold state.
func (f *Folder) AdmissionProfile(genesis string) (AdmissionProfile, error) {
	if f == nil || f.state == nil {
		return AdmissionProfile{}, errors.New("admission profile requires a fold prefix")
	}
	retired := f.state.retired()
	for index := len(f.state.records) - 1; index >= 0; index-- {
		record := &f.state.records[index]
		state, ok := record.body.(*State)
		if !ok || state.Kind != KindAdmissionProfile || record.decision.Verdict != Effective {
			continue
		}
		if state.Body["genesis"] != genesis || retired[record.record.ID] || !f.state.ratified(record.record.ID, retired) {
			continue
		}
		return AdmissionProfile{
			Event: record.record.ID, Bundle: state.Body["bundle"],
			Contract: state.Body["contract"], Genesis: genesis,
		}, nil
	}
	bundle, err := bootstrapAdmissionProfileOID(genesis)
	if err != nil {
		return AdmissionProfile{}, err
	}
	return AdmissionProfile{
		Bundle: bundle, Contract: GenesisAdmissionContract,
		Genesis: genesis, Bootstrap: true,
	}, nil
}

// ResolveAdmissionProfile keeps interpreter availability distinct from fold
// validity. A missing interpreter is profile-unavailable: it is neither a
// successful reconciliation nor a fault attributed to the sequencer.
func ResolveAdmissionProfile(records []Record, genesis string, interpreterAvailable func(contract string) bool) (AdmissionProfileResolution, error) {
	profile, err := SelectAdmissionProfile(records, genesis)
	if err != nil {
		return AdmissionProfileResolution{}, err
	}
	status := AdmissionProfileUnavailable
	if interpreterAvailable != nil && interpreterAvailable(profile.Contract) {
		status = AdmissionProfileAvailable
	}
	return AdmissionProfileResolution{Profile: profile, Status: status}, nil
}

func bootstrapAdmissionProfileOID(genesis string) (string, error) {
	decoded, err := hex.DecodeString(genesis)
	if err != nil || hex.EncodeToString(decoded) != genesis {
		return "", errors.New("genesis must be canonical lowercase hex")
	}
	var emptyTree string
	switch len(decoded) {
	case sha1.Size:
		emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	case sha256.Size:
		emptyTree = "6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321"
	default:
		return "", fmt.Errorf("unsupported genesis object format with %d-byte hash", len(decoded))
	}
	body := fmt.Sprintf("tree %s\nauthor gitseq <gitseq@invalid> 0 +0000\ncommitter gitseq <gitseq@invalid> 0 +0000\n\ngitseq-admission-profile-v0 %s\n", emptyTree, genesis)
	object := append([]byte(fmt.Sprintf("commit %d\x00", len(body))), body...)
	if len(decoded) == sha1.Size {
		digest := sha1.Sum(object)
		return hex.EncodeToString(digest[:]), nil
	}
	digest := sha256.Sum256(object)
	return hex.EncodeToString(digest[:]), nil
}
