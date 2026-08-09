package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Resident says where a repository's resident service is listening. The record
// sits beside the workroom config inside the repository's own git directory,
// so a client finds the service by naming the repository instead of being
// told a URL out of band. The genesis travels with the address because an
// address alone cannot say which workroom answers there.
type Resident struct {
	URL     string `json:"url"`
	Genesis string `json:"genesis"`
	PID     int    `json:"pid"`
}

const residentFile = "resident.json"

// PublishResident advertises this process as the repository's resident service
// and returns the withdrawal. It is not a lock: the last writer wins, and a
// record left behind by a dead process costs a client only a refused
// connection before it falls back to acting locally.
func (w *Workspace) PublishResident(url string) (withdraw func(), err error) {
	record := Resident{URL: strings.TrimRight(url, "/"), Genesis: w.Config.Genesis, PID: os.Getpid()}
	content, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(w.MetaDir, residentFile)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, path); err != nil {
		return nil, err
	}
	return func() {
		// Only withdraw our own advertisement. A later service that took the
		// repository over is still serving it, and removing its record would
		// send clients into degraded mode for no reason.
		if current, ok := readResident(path); ok && current.PID == record.PID {
			_ = os.Remove(path)
		}
	}, nil
}

// ResidentURL names the service holding this workroom, when one has published
// itself. A record naming a different genesis was left by a workroom this
// repository no longer has, and is refused rather than trusted: acting through
// it would append to another log.
func (w *Workspace) ResidentURL() (string, bool) {
	record, ok := readResident(filepath.Join(w.MetaDir, residentFile))
	if !ok || record.URL == "" || record.Genesis != w.Config.Genesis {
		return "", false
	}
	return record.URL, true
}

func readResident(path string) (Resident, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Resident{}, false
	}
	var record Resident
	if err := json.Unmarshal(content, &record); err != nil {
		return Resident{}, false
	}
	return record, true
}
