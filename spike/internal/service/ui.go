package service

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"

	"gitseq/spike/internal/app"
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

// handleActors lists CUSTODIAL identities this service can sign for — who a
// browser may join as. The folded roster (names, roles, revocations) is the
// projection's business; these are deliberately separate concepts.
func (s *Server) handleActors(writer http.ResponseWriter, request *http.Request) {
	views, err := s.workspace.ActorViews(request.Context())
	actors := make([]app.ActorView, 0, len(views))
	for _, actor := range views {
		if actor.Custody {
			actors = append(actors, actor)
		}
	}
	write(writer, actors, err)
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
	actorName, present := s.hub.SessionActor(input.Session)
	if !present {
		write(writer, nil, errors.New("session is not present"))
		return
	}

	var attachments map[string][]byte
	if len(input.Evidence) > 0 {
		attachments = make(map[string][]byte, len(input.Evidence))
		for name, content := range input.Evidence {
			attachments[name] = []byte(content)
		}
	}
	submission, err := s.workspace.Act(request.Context(), actorName, app.Act{
		Verb: app.Verb(input.Act), Kind: workroom.Kind(input.Kind), Text: input.Text,
		Body: input.Body, Target: input.Target, RestsOn: input.RestsOn,
		Attachments: attachments, IdempotencyKey: input.IdempotencyKey,
	})
	write(writer, submission.Record, err)
}
