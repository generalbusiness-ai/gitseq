package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/service"
	"gitseq/spike/internal/workroom"
)

type values []string

func (v *values) String() string { return strings.Join(*v, ",") }
func (v *values) Set(value string) error {
	*v = append(*v, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "init":
		err = initCommand(ctx, os.Args[2:])
	case "actor-add":
		err = actorAddCommand(ctx, os.Args[2:])
	case "role-grant":
		err = roleGrantCommand(ctx, os.Args[2:])
	case "role-revoke":
		err = roleRevokeCommand(ctx, os.Args[2:])
	case "actors":
		err = actorsCommand(ctx, os.Args[2:])
	case "state":
		err = stateCommand(ctx, os.Args[2:])
	case "ratify":
		err = ratifyCommand(ctx, os.Args[2:])
	case "supersede":
		err = supersedeCommand(ctx, os.Args[2:])
	case "status":
		err = statusCommand(ctx, os.Args[2:])
	case "provenance":
		err = provenanceCommand(ctx, os.Args[2:])
	case "verify":
		err = verifyCommand(ctx, os.Args[2:])
	case "serve":
		err = serveCommand(ctx, os.Args[2:])
	case "attach":
		err = attachCommand(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gs:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gs <init|actor-add|role-grant|role-revoke|actors|state|ratify|supersede|status|provenance|verify|serve|attach> [flags]")
	os.Exit(2)
}

func flags(name string, arguments []string) (*flag.FlagSet, *string) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	repo := set.String("repo", ".", "ordinary Git repository")
	set.SetOutput(io.Discard)
	return set, repo
}

func initCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("init", arguments)
	operator := set.String("operator", "operator", "operator actor name")
	ceiling := set.Uint64("payload-ceiling", 1<<20, "inline payload ceiling")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, seed, err := app.Init(ctx, *repo, *operator, *ceiling)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"genesis": workspace.Config.Genesis, "operator": workspace.Config.Actors[*operator], "seed": seed.ID})
}

func actorAddCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("actor-add", arguments)
	as := set.String("as", "operator", "operator actor")
	name := set.String("name", "", "new actor name")
	kind := set.String("kind", "agent", "principal kind: human, agent, or service")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	actor, records, err := workspace.AddActor(ctx, *as, *name, *kind)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"actor": actor, "events": []string{records[0].ID, records[1].ID}})
}

func roleGrantCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("role-grant", arguments)
	as := set.String("as", "operator", "granting actor")
	actor := set.String("actor", "", "actor name, @name, or fingerprint")
	role := set.String("role", "", "durable authority role")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	records, err := workspace.GrantRole(ctx, *as, *actor, *role)
	if err != nil {
		return err
	}
	events := make([]string, 0, len(records))
	for _, record := range records {
		events = append(events, record.ID)
	}
	return printJSON(map[string]any{"actor": *actor, "role": *role, "events": events})
}

func roleRevokeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("role-revoke", arguments)
	as := set.String("as", "operator", "revoking actor")
	actor := set.String("actor", "", "actor name, @name, or fingerprint")
	role := set.String("role", "", "durable authority role")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	records, err := workspace.RevokeRole(ctx, *as, *actor, *role)
	if err != nil {
		return err
	}
	events := make([]string, 0, len(records))
	for _, record := range records {
		events = append(events, record.ID)
	}
	return printJSON(map[string]any{"actor": *actor, "role": *role, "events": events})
}

func actorsCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("actors", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	actors, err := workspace.ActorViews(ctx)
	if err != nil {
		return err
	}
	return printJSON(actors)
}

func stateCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("state", arguments)
	as := set.String("as", "", "actor name")
	kind := set.String("kind", "", "statement kind")
	message := set.String("text", "", "statement text")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	var bodyValues, rests, evidence values
	set.Var(&bodyValues, "body", "body key=value (repeatable)")
	set.Var(&rests, "rests-on", "causal event id (repeatable)")
	set.Var(&evidence, "evidence", "attachment name=path (repeatable)")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	body, err := pairs(bodyValues)
	if err != nil {
		return err
	}
	attachments, err := files(evidence)
	if err != nil {
		return err
	}
	record, err := submitAct(ctx, workspace, *serverURL, *as, app.Act{Verb: app.VerbState, Kind: workroom.Kind(*kind), Text: *message, Body: body, RestsOn: rests, Attachments: attachments, IdempotencyKey: *key})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func ratifyCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("ratify", arguments)
	as := set.String("as", "", "actor name")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("ratify requires one target event")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	target := set.Arg(0)
	record, err := submitAct(ctx, workspace, *serverURL, *as, app.Act{Verb: app.VerbRatify, Target: target, IdempotencyKey: *key})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func supersedeCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("supersede", arguments)
	as := set.String("as", "", "actor name")
	message := set.String("text", "", "reason")
	serverURL := set.String("server", "", "resident sequencer URL")
	key := set.String("idempotency-key", "", "stable retry key")
	var rests values
	set.Var(&rests, "rests-on", "additional causal event id")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("supersede requires one target event")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	target := set.Arg(0)
	record, err := submitAct(ctx, workspace, *serverURL, *as, app.Act{Verb: app.VerbSupersede, Target: target, Text: *message, RestsOn: rests, IdempotencyKey: *key})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

func submitAct(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, act app.Act) (workroom.Record, error) {
	if serverURL == "" {
		submission, err := workspace.Act(ctx, actorName, act)
		return submission.Record, err
	}
	_, private, err := workspace.Actor(actorName)
	if err != nil {
		return workroom.Record{}, err
	}
	request, err := workspace.BuildActRequest(ctx, private, actorName, act)
	if err != nil {
		return workroom.Record{}, err
	}
	encoded, _ := json.Marshal(request)
	response, err := http.Post(strings.TrimRight(serverURL, "/")+"/v0/submit", "application/json", bytes.NewReader(encoded))
	if err != nil {
		return workroom.Record{}, err
	}
	defer response.Body.Close()
	var result struct {
		app.Submission
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return workroom.Record{}, err
	}
	if response.StatusCode != http.StatusOK {
		return workroom.Record{}, errors.New(result.Error)
	}
	return result.Record, nil
}

func statusCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("status", arguments)
	jsonOutput := set.Bool("json", false, "render JSON")
	serverURL := set.String("server", "", "workroom URL including live state")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *serverURL != "" {
		response, err := http.Get(strings.TrimRight(*serverURL, "/") + "/v0/status")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, err = io.Copy(os.Stdout, response.Body)
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(snapshot)
	}
	_, err = os.Stdout.Write(workroom.RenderStatus(snapshot.Projection))
	return err
}

func provenanceCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("provenance", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("provenance requires one event")
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(workroom.RenderProvenance(snapshot.Projection, set.Arg(0)))
	return err
}

func verifyCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("verify", arguments)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	verification, err := workspace.Verify(ctx)
	if err != nil {
		return err
	}
	return printJSON(verification)
}

func serveCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("serve", arguments)
	listen := set.String("listen", "127.0.0.1:7777", "HTTP listen address")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if err := validateLoopbackListen(*listen); err != nil {
		return err
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	if workspace.Config.ReadOnly {
		return errors.New("cannot serve a read-only attachment")
	}
	server, err := service.New(workspace)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gitseq workroom http://%s\n", *listen)
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}
	return httpServer.ListenAndServe()
}

func validateLoopbackListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid --listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("--listen must name a loopback address; the resident service is a trusted local multi-actor custodian")
	}
	return nil
}

func attachCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("attach", arguments)
	remote := set.String("remote", "origin", "Git remote")
	genesis := set.String("genesis", "", "workroom genesis hash")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *genesis == "" {
		return errors.New("attach requires --genesis")
	}
	refspec := "+refs/seq/*:refs/seq/*"
	existing, _ := git(ctx, *repo, "config", "--get-all", "remote."+*remote+".fetch")
	if !containsLine(existing, refspec) {
		if _, err := git(ctx, *repo, "config", "--add", "remote."+*remote+".fetch", refspec); err != nil {
			return err
		}
	}
	if _, err := git(ctx, *repo, "fetch", *remote, refspec); err != nil {
		return err
	}
	formatOutput, err := git(ctx, *repo, "rev-parse", "--show-object-format")
	if err != nil {
		return err
	}
	workspace, err := app.AttachConfig(ctx, *repo, *genesis, strings.TrimSpace(formatOutput))
	if err != nil {
		return err
	}
	verification, err := workspace.Verify(ctx)
	if err != nil {
		return err
	}
	return printJSON(verification)
}

func git(ctx context.Context, repo string, arguments ...string) (string, error) {
	args := append([]string{"-C", repo}, arguments...)
	output, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func containsLine(content, line string) bool {
	for _, candidate := range strings.Split(content, "\n") {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}
	return false
}

func pairs(items []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("expected key=value, got %q", item)
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func files(items []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for _, item := range items {
		name, path, ok := strings.Cut(item, "=")
		if !ok || filepath.Base(name) != name {
			return nil, fmt.Errorf("expected safe-name=path, got %q", item)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		result[name] = content
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
