package service

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
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
	Commits   []gitstore.GraphCommit `json:"commits"`
	Truncated bool                   `json:"truncated,omitempty"`
}

func (s *Server) handleGraph(writer http.ResponseWriter, request *http.Request) {
	commits, err := s.workspace.Store.Graph(request.Context(), 81)
	truncated := len(commits) > 80
	if truncated {
		commits = commits[:80]
	}
	if commits == nil {
		commits = []gitstore.GraphCommit{}
	}
	write(writer, graphResponse{Commits: commits, Truncated: truncated}, err)
}

// landedRequest asks whether a branch already carries the named commits. The
// browser names commits; it never names the branch, because a branch name is
// a ref this process resolves and a commit is a value it can validate.
type landedRequest struct {
	Commits []string `json:"commits"`
}

type landedResponse struct {
	// Which ref the answers are about, so the page can say "landed on main"
	// rather than inventing the name. Empty when no candidate ref resolved.
	Branch  string             `json:"branch"`
	Commits []gitstore.Landing `json:"commits"`
}

// The mainline, in the order a repository is likely to name it. Resolved here
// rather than configured: the question this answers is "did it ship", and
// shipping means reaching the branch the repository publishes.
var mainlineRefs = []string{"refs/heads/main", "refs/heads/master"}

// handleLanded joins the fold's answer to git's. The fold knows whether a
// commitment closed; only git knows whether the code landed, and work that
// shipped and stayed open is invisible on every other surface. Nothing here is
// stored: a field somebody types can be stale by hand, and this one was.
func (s *Server) handleLanded(writer http.ResponseWriter, request *http.Request) {
	var input landedRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	// One cap, before any path. The advertised bound belongs to the endpoint,
	// not to whichever branch happens to answer: a limit enforced only on the
	// path that succeeds is not a limit, and the failure path is the easier
	// one to reach with untrusted input.
	if len(input.Commits) > gitstore.LandingLimit {
		input.Commits = input.Commits[:gitstore.LandingLimit]
	}
	branch := ""
	for _, ref := range mainlineRefs {
		if _, present, err := s.workspace.Store.RefValue(request.Context(), ref); err == nil && present {
			branch = ref
			break
		}
	}
	if branch == "" {
		// No mainline to compare against is not "nothing landed". Every answer
		// is unknown, and says so.
		commits := make([]gitstore.Landing, 0, len(input.Commits))
		for _, commit := range input.Commits {
			commits = append(commits, gitstore.Landing{Commit: commit, Status: gitstore.LandingUnknown, Reason: "no mainline branch"})
		}
		write(writer, landedResponse{Commits: commits}, nil)
		return
	}
	write(writer, landedResponse{
		Branch:  strings.TrimPrefix(branch, "refs/heads/"),
		Commits: s.workspace.Store.Landings(request.Context(), branch, input.Commits),
	}, nil)
}

type worktreesResponse struct {
	Repo      string             `json:"repo"`
	Worktrees []app.WorktreeView `json:"worktrees"`
}

func (s *Server) handleWorktrees(writer http.ResponseWriter, request *http.Request) {
	local, err := s.workspace.LocalWorktrees(request.Context())
	if local.Worktrees == nil {
		local.Worktrees = []app.WorktreeView{}
	}
	write(writer, worktreesResponse{Repo: local.Path, Worktrees: local.Worktrees}, err)
}

// actRequest is a session-bound durable act: the same custody model as
// session-bound speech, extended to the three workroom verbs. The service
// signs with the custodial key of the actor the session announced as.
type actRequest struct {
	Session        string            `json:"credential"`
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
		write(writer, nil, errors.New("credential is required"))
		return
	}
	actorName, present := s.hub.SessionActor(input.Session)
	if !present {
		write(writer, nil, errors.New("credential is not valid"))
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
