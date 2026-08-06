// Package app joins the semantic-free kernel to the workroom profile for a
// single ordinary Git repository.
package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
	"gitseq/spike/internal/kernel"
	"gitseq/spike/internal/workroom"
)

type Actor struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Role        string `json:"role"`
	KeyFile     string `json:"key_file"`
}

type Config struct {
	Version        int              `json:"version"`
	Genesis        string           `json:"genesis"`
	ObjectFormat   string           `json:"object_format"`
	PayloadCeiling uint64           `json:"payload_ceiling"`
	SequencerKey   string           `json:"sequencer_key,omitempty"`
	ReadOnly       bool             `json:"read_only,omitempty"`
	Actors         map[string]Actor `json:"actors,omitempty"`
}

type Workspace struct {
	Repo    string
	GitDir  string
	MetaDir string
	Store   gitstore.Store
	Config  Config
}

type Snapshot struct {
	Genesis    string              `json:"genesis"`
	Head       string              `json:"head"`
	Depth      int                 `json:"depth"`
	Projection workroom.Projection `json:"projection"`
}

func ResolveGitDir(ctx context.Context, repo string) (string, error) {
	if repo == "" {
		repo = "."
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--absolute-git-dir")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve git dir: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func Open(ctx context.Context, repo string) (*Workspace, error) {
	gitDir, err := ResolveGitDir(ctx, repo)
	if err != nil {
		return nil, err
	}
	metaDir := filepath.Join(gitDir, "gitseq")
	content, err := os.ReadFile(filepath.Join(metaDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read gitseq config (run `gs init` first): %w", err)
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, err
	}
	if config.Version != 0 || config.Genesis == "" || config.ObjectFormat == "" || (!config.ReadOnly && config.SequencerKey == "") {
		return nil, errors.New("invalid gitseq config")
	}
	return &Workspace{Repo: repo, GitDir: gitDir, MetaDir: metaDir, Store: gitstore.Store{Repo: gitDir}, Config: config}, nil
}

func Init(ctx context.Context, repo, operatorName string, ceiling uint64) (*Workspace, workroom.Record, error) {
	if operatorName == "" {
		operatorName = "operator"
	}
	if ceiling == 0 {
		ceiling = 1 << 20
	}
	gitDir, err := ResolveGitDir(ctx, repo)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	metaDir := filepath.Join(gitDir, "gitseq")
	if _, err := os.Stat(filepath.Join(metaDir, "config.json")); err == nil {
		return nil, workroom.Record{}, errors.New("workroom already initialized")
	}
	if err := os.MkdirAll(filepath.Join(metaDir, "actors"), 0o700); err != nil {
		return nil, workroom.Record{}, err
	}
	store := gitstore.Store{Repo: gitDir}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	sequencerKey := filepath.Join(metaDir, "sequencer")
	publicKey, err := gitstore.GenerateSSHKey(ctx, sequencerKey)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	private, fingerprint, actorPath, err := generateActor(filepath.Join(metaDir, "actors"), operatorName)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	genesis, err := kernel.Create(ctx, store, kernel.GenesisDescriptor{Version: 0, ObjectFormat: format, PayloadCeiling: ceiling, SequencerPublicKey: publicKey}, sequencerKey)
	if err != nil {
		return nil, workroom.Record{}, err
	}
	workspace := &Workspace{Repo: repo, GitDir: gitDir, MetaDir: metaDir, Store: store, Config: Config{
		Version: 0, Genesis: genesis, ObjectFormat: format, PayloadCeiling: ceiling,
		SequencerKey: sequencerKey, Actors: map[string]Actor{operatorName: {Name: operatorName, Fingerprint: fingerprint, Role: "operator", KeyFile: actorPath}},
	}}
	payload := workroom.State{Kind: workroom.KindRoster, Text: operatorName + " begins the workroom", Body: map[string]string{"actor": fingerprint, "name": operatorName, "role": "operator"}}
	result, err := workspace.submitWithPrivate(ctx, private, operatorName, workroom.SchemaState, payload, nil, nil, "bootstrap")
	if err != nil {
		return nil, workroom.Record{}, err
	}
	if err := workspace.save(); err != nil {
		return nil, workroom.Record{}, err
	}
	record, err := workspace.Record(ctx, result.Commit)
	return workspace, record, err
}

func generateActor(directory, name string) (ed25519.PrivateKey, string, string, error) {
	if name == "" || strings.ContainsAny(name, "/\\\x00\r\n") {
		return nil, "", "", errors.New("invalid actor name")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", "", err
	}
	path := filepath.Join(directory, name+".key")
	if _, err := os.Stat(path); err == nil {
		return nil, "", "", errors.New("actor key already exists")
	}
	if err := os.WriteFile(path, []byte(base64.RawURLEncoding.EncodeToString(private)+"\n"), 0o600); err != nil {
		return nil, "", "", err
	}
	return private, intent.ActorFingerprint(private.Public().(ed25519.PublicKey)), path, nil
}

func readActor(path string) (ed25519.PrivateKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(content)))
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid actor private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func (w *Workspace) save() error {
	content, err := json.MarshalIndent(w.Config, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(w.MetaDir, "config.json.tmp")
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(w.MetaDir, "config.json"))
}

func AttachConfig(repo, gitDir, genesis, objectFormat string) (*Workspace, error) {
	metaDir := filepath.Join(gitDir, "gitseq")
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		return nil, err
	}
	workspace := &Workspace{Repo: repo, GitDir: gitDir, MetaDir: metaDir, Store: gitstore.Store{Repo: gitDir}, Config: Config{Version: 0, Genesis: genesis, ObjectFormat: objectFormat, ReadOnly: true}}
	if err := workspace.save(); err != nil {
		return nil, err
	}
	return workspace, nil
}

func (w *Workspace) Actor(name string) (Actor, ed25519.PrivateKey, error) {
	actor, ok := w.Config.Actors[name]
	if !ok {
		return Actor{}, nil, fmt.Errorf("unknown actor %q", name)
	}
	private, err := readActor(actor.KeyFile)
	return actor, private, err
}

func (w *Workspace) AddActor(ctx context.Context, operatorName, name, role string) (Actor, []workroom.Record, error) {
	if _, exists := w.Config.Actors[name]; exists {
		return Actor{}, nil, fmt.Errorf("actor %q already exists", name)
	}
	if role == "" {
		role = "agent"
	}
	private, fingerprint, path, err := generateActor(filepath.Join(w.MetaDir, "actors"), name)
	if err != nil {
		return Actor{}, nil, err
	}
	_ = private
	actor := Actor{Name: name, Fingerprint: fingerprint, Role: role, KeyFile: path}
	state, err := w.Submit(ctx, operatorName, workroom.SchemaState, workroom.State{Kind: workroom.KindRoster, Text: name + " joins as " + role, Body: map[string]string{"actor": fingerprint, "name": name, "role": role}}, []string{w.EventID(w.Config.Genesis)}, nil, "actor-"+name)
	if err != nil {
		return Actor{}, nil, err
	}
	ratification, err := w.Submit(ctx, operatorName, workroom.SchemaRatify, workroom.Ratify{Target: state.ID}, []string{state.ID}, nil, "actor-"+name+"-ratify")
	if err != nil {
		return Actor{}, nil, err
	}
	w.Config.Actors[name] = actor
	if err := w.save(); err != nil {
		return Actor{}, nil, err
	}
	return actor, []workroom.Record{state, ratification}, nil
}

func (w *Workspace) Submit(ctx context.Context, actorName, schema string, payload any, rests []string, attachments map[string][]byte, key string) (workroom.Record, error) {
	_, private, err := w.Actor(actorName)
	if err != nil {
		return workroom.Record{}, err
	}
	request, err := w.BuildRequest(ctx, private, actorName, schema, payload, rests, attachments, key)
	if err != nil {
		return workroom.Record{}, err
	}
	result, err := w.Accept(ctx, request)
	if err != nil {
		return workroom.Record{}, err
	}
	return w.Record(ctx, result.Commit)
}

func (w *Workspace) submitWithPrivate(ctx context.Context, private ed25519.PrivateKey, actorName, schema string, payload any, rests []string, attachments map[string][]byte, key string) (kernel.Result, error) {
	request, err := w.BuildRequest(ctx, private, actorName, schema, payload, rests, attachments, key)
	if err != nil {
		return kernel.Result{}, err
	}
	return w.Accept(ctx, request)
}

func (w *Workspace) BuildRequest(ctx context.Context, private ed25519.PrivateKey, actorName, schema string, payload any, rests []string, attachments map[string][]byte, key string) (kernel.Request, error) {
	encoded, err := workroom.Encode(payload)
	if err != nil {
		return kernel.Request{}, err
	}
	tree, err := w.Store.WritePayloadTree(ctx, encoded, attachments)
	if err != nil {
		return kernel.Request{}, err
	}
	if key == "" {
		key, err = randomKey()
		if err != nil {
			return kernel.Request{}, err
		}
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + w.Config.ObjectFormat + ":" + w.Config.Genesis,
		Schema: schema, PayloadTree: "git:" + w.Config.ObjectFormat + ":" + tree,
		RestsOn: rests, IdempotencyNS: "gs/" + actorName, IdempotencyKey: key,
	}, private)
	if err != nil {
		return kernel.Request{}, err
	}
	return kernel.Request{Signed: signed, Payload: encoded, Attachments: attachments}, nil
}

func (w *Workspace) Accept(ctx context.Context, request kernel.Request) (kernel.Result, error) {
	if w.Config.ReadOnly {
		return kernel.Result{}, errors.New("attached workroom is read-only; configure local custody and a sequencer endpoint to submit")
	}
	return kernel.Submit(ctx, w.Store, request, kernel.Options{SigningKey: w.Config.SequencerKey, PreAppend: w.allowlist})
}

func (w *Workspace) allowlist(_ context.Context, admission kernel.Admission) error {
	fingerprint := intent.ActorFingerprint(admission.ActorKey)
	for _, actor := range w.Config.Actors {
		if actor.Fingerprint == fingerprint {
			return nil
		}
	}
	// The first operator event is submitted before config is persisted.
	if len(w.Config.Actors) == 1 {
		for _, actor := range w.Config.Actors {
			if actor.Fingerprint == fingerprint && actor.Role == "operator" {
				return nil
			}
		}
	}
	return errors.New("actor is not in the static allowlist")
}

func randomKey() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func (w *Workspace) EventID(commit string) string {
	return "git:" + w.Config.ObjectFormat + ":" + w.Config.Genesis + "#git:" + w.Config.ObjectFormat + ":" + commit
}

func (w *Workspace) Record(ctx context.Context, commit string) (workroom.Record, error) {
	events, _, err := kernel.Load(ctx, w.Store, w.Config.Genesis)
	if err != nil {
		return workroom.Record{}, err
	}
	for _, event := range events {
		if event.Commit == commit {
			return workroom.Record{ID: w.EventID(commit), Actor: intent.ActorFingerprint(event.Signed.ActorKey), Schema: event.Intent.Schema, RestsOn: append([]string(nil), event.Intent.RestsOn...), Payload: event.Payload, Attachments: event.Attachments}, nil
		}
	}
	return workroom.Record{}, errors.New("event not found")
}

func (w *Workspace) Snapshot(ctx context.Context) (Snapshot, error) {
	events, verification, err := kernel.Load(ctx, w.Store, w.Config.Genesis)
	if err != nil {
		return Snapshot{}, err
	}
	records := make([]workroom.Record, 0, len(events))
	for _, event := range events {
		records = append(records, workroom.Record{ID: w.EventID(event.Commit), Actor: intent.ActorFingerprint(event.Signed.ActorKey), Schema: event.Intent.Schema, RestsOn: event.Intent.RestsOn, Payload: event.Payload, Attachments: event.Attachments})
	}
	return Snapshot{Genesis: verification.Genesis, Head: verification.Head, Depth: verification.Depth, Projection: workroom.Fold(records)}, nil
}

func (w *Workspace) Verify(ctx context.Context) (kernel.Verification, error) {
	return kernel.Verify(ctx, w.Store, w.Config.Genesis)
}

func (w *Workspace) ActorNames() []string {
	names := make([]string, 0, len(w.Config.Actors))
	for name := range w.Config.Actors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func HashEvidence(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}
