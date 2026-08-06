package service

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/workroom"
)

// The built chat-first UI. Committed build output so `go build` needs no
// Node toolchain; regenerate with `make ui`.
//
//go:embed uidist
var uidist embed.FS

func uiHandler() http.Handler {
	dist, err := fs.Sub(uidist, "uidist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			if _, err := fs.Stat(dist, request.URL.Path[1:]); err != nil {
				http.NotFound(writer, request)
				return
			}
		}
		writer.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(writer, request)
	})
}

type actorSummary struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Role        string `json:"role"`
}

func (s *Server) handleActors(writer http.ResponseWriter, _ *http.Request) {
	actors := make([]actorSummary, 0)
	for _, name := range s.workspace.ActorNames() {
		actor := s.workspace.Config.Actors[name]
		actors = append(actors, actorSummary{Name: actor.Name, Fingerprint: actor.Fingerprint, Role: actor.Role})
	}
	write(writer, actors, nil)
}

type graphResponse struct {
	Commits []gitstore.GraphCommit `json:"commits"`
}

func (s *Server) handleGraph(writer http.ResponseWriter, request *http.Request) {
	commits, err := s.workspace.Store.Graph(request.Context(), 80)
	if commits == nil {
		commits = []gitstore.GraphCommit{}
	}
	write(writer, graphResponse{Commits: commits}, err)
}

// actRequest is a session-bound durable act: the same custody model as
// session-bound speech, extended to the three workroom verbs. The service
// signs with the custodial key of the actor the session announced as.
type actRequest struct {
	Session        string            `json:"session"`
	Act            string            `json:"act"` // state | ratify | supersede
	Kind           string            `json:"kind,omitempty"`
	Text           string            `json:"text,omitempty"`
	Body           map[string]string `json:"body,omitempty"`
	Target         string            `json:"target,omitempty"`
	RestsOn        []string          `json:"rests_on,omitempty"`
	Evidence       map[string]string `json:"evidence,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

func (s *Server) handleAct(writer http.ResponseWriter, request *http.Request) {
	var input actRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	if input.Session == "" {
		write(writer, nil, errors.New("session is required"))
		return
	}
	s.mu.Lock()
	actorName, exists := s.sessions[input.Session]
	present := exists && s.hub.HasPresence(input.Session)
	s.mu.Unlock()
	if !present {
		write(writer, nil, errors.New("session is not present"))
		return
	}

	var schema string
	var payload any
	rests := input.RestsOn
	switch input.Act {
	case "state":
		schema = workroom.SchemaState
		payload = workroom.State{Kind: workroom.Kind(input.Kind), Text: input.Text, Body: input.Body}
	case "ratify":
		schema = workroom.SchemaRatify
		payload = workroom.Ratify{Target: input.Target}
		rests = []string{input.Target}
	case "supersede":
		schema = workroom.SchemaSupersede
		payload = workroom.Supersede{Target: input.Target, Text: input.Text}
		rests = append([]string{input.Target}, rests...)
	default:
		write(writer, nil, errors.New("act must be state, ratify, or supersede"))
		return
	}
	var attachments map[string][]byte
	if len(input.Evidence) > 0 {
		attachments = make(map[string][]byte, len(input.Evidence))
		for name, content := range input.Evidence {
			attachments[name] = []byte(content)
		}
	}
	record, err := s.workspace.Submit(request.Context(), actorName, schema, payload, rests, attachments, input.IdempotencyKey)
	write(writer, record, err)
}
