package nexus

import (
	"strings"
	"testing"
	"time"
)

func TestResidentMintsOpaqueCredentialAndRevokesIt(t *testing.T) {
	hub, err := New(32)
	if err != nil {
		t.Fatal(err)
	}
	credential, change, err := hub.OpenSessionIdentity("alice", "actor:alice", "alice", time.Minute, ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if !validCredential(credential) || len(strings.TrimPrefix(credential, credentialPrefix)) != credentialBytes*2 {
		t.Fatalf("resident minted malformed credential %q", credential)
	}
	if credential == change.ID || credential == hub.HandleFor(credential) {
		t.Fatal("private credential was reused as its public handle")
	}
	if _, err := hub.RenewSessionIdentity(credential, "alice", "actor:alice", "alice", time.Minute, ActivityUpdate{}); err != nil {
		t.Fatalf("owner could not renew: %v", err)
	}
	for _, mismatch := range []struct{ actor, fingerprint string }{{"bob", "actor:alice"}, {"alice", "actor:bob"}} {
		if _, err := hub.RenewSessionIdentity(credential, mismatch.actor, mismatch.fingerprint, "other", time.Minute, ActivityUpdate{}); err == nil {
			t.Fatal("credential crossed its actor binding")
		}
	}
	if _, err := hub.RevokeSession(credential); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.RenewSessionIdentity(credential, "alice", "actor:alice", "alice", time.Minute, ActivityUpdate{}); err == nil {
		t.Fatal("revoked credential renewed")
	}
	if _, err := hub.RevokeSession(credential); err == nil {
		t.Fatal("revoked credential was replayed")
	}
}

func TestCredentialsAreResidentScopedAndExpire(t *testing.T) {
	first, _ := New(32)
	second, _ := New(32)
	credential, _, err := first.OpenSessionIdentity("alice", "actor:alice", "alice", time.Nanosecond, ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.RenewSessionIdentity(credential, "alice", "actor:alice", "alice", time.Minute, ActivityUpdate{}); err == nil {
		t.Fatal("credential crossed a resident/repository boundary")
	}
	setHubNow(first, time.Now().Add(time.Second))
	if _, err := first.RenewSessionIdentity(credential, "alice", "actor:alice", "alice", time.Minute, ActivityUpdate{}); err == nil {
		t.Fatal("expired credential renewed")
	}
}
