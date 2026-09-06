package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type previewRequest struct {
	Event      string `json:"event"`
	Path       string `json:"path,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Attachment string `json:"attachment,omitempty"`
}

type previewResponse struct {
	Repo        string   `json:"repo"`
	Event       string   `json:"event"`
	Commit      string   `json:"commit,omitempty"`
	Path        string   `json:"path,omitempty"`
	Status      string   `json:"status"`
	Message     string   `json:"message,omitempty"`
	Content     string   `json:"content,omitempty"`
	Entries     []string `json:"entries,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
	Omitted     int      `json:"omitted,omitempty"`
	Heads       []string `json:"heads,omitempty"`
	Limit       int      `json:"limit"`
}

func (s *Server) handlePreview(writer http.ResponseWriter, request *http.Request) {
	// Independent of the signed-write budget; these are bounded read requests.
	select {
	case s.previewSlots <- struct{}{}:
		defer func() { <-s.previewSlots }()
	default:
		http.Error(writer, "preview busy", http.StatusServiceUnavailable)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 8192)
	var input previewRequest
	if err := decodePreview(request, &input); err != nil {
		write(writer, nil, errors.New("invalid preview request"))
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	snapshot, err := s.workspace.Snapshot(ctx)
	if err != nil {
		write(writer, nil, errors.New("workroom unavailable"))
		return
	}
	result := previewResponse{Repo: s.workspace.Repo, Event: input.Event, Status: "ready", Limit: gitstore.PreviewContentLimit}
	fail := func(status, message string) {
		result.Status = status
		result.Message = message
		write(writer, result, nil)
	}
	known := false
	for _, d := range snapshot.Projection.Decisions {
		if d.Event == input.Event {
			known = true
			break
		}
	}
	if !known {
		fail("missing", "This record is not in the selected workroom.")
		return
	}
	config := s.workspace.View()
	prefix := "git:" + config.ObjectFormat + ":" + snapshot.Genesis + "#git:" + config.ObjectFormat + ":"
	if !strings.HasPrefix(input.Event, prefix) {
		fail("missing", "This record belongs to another workroom.")
		return
	}
	eventCommit := strings.TrimPrefix(input.Event, prefix)
	result.Heads = previewHeads(snapshot.Projection, input.Event, config.ObjectFormat)
	if input.Path != "" && input.Attachment != "" || input.Path == "" && input.Commit != "" {
		fail("invalid", "Choose one cited file or evidence attachment.")
		return
	}
	var object gitstore.PreviewObject
	switch {
	case input.Attachment != "":
		if strings.ContainsAny(input.Attachment, "/\\") || !gitstore.ValidPreviewPath(input.Attachment) {
			fail("invalid", "Invalid evidence name.")
			return
		}
		listing, listErr := s.workspace.Store.Preview(ctx, config.ObjectFormat, eventCommit, "attachments")
		if listErr == nil && listing.Directory {
			result.Attachments = listing.Entries
			result.Omitted = listing.Omitted
		}
		result.Commit = eventCommit
		result.Path = input.Attachment
		object, err = s.workspace.Store.Preview(ctx, config.ObjectFormat, eventCommit, "attachments/"+input.Attachment)
	case input.Path != "":
		result.Path = input.Path
		if !gitstore.ValidPreviewPath(input.Path) {
			fail("invalid", "Use a literal path inside this repository.")
			return
		}
		chosen := input.Commit
		if chosen == "" {
			if len(result.Heads) == 0 {
				fail("unavailable", "This record cites no exact source revision.")
				return
			}
			if len(result.Heads) > 1 {
				fail("ambiguous", "This record cites several revisions. Choose the exact cited revision.")
				return
			}
			chosen = result.Heads[0]
		}
		allowed := false
		for _, head := range result.Heads {
			if head == chosen {
				allowed = true
			}
		}
		if !allowed {
			fail("invalid", "That exact revision is not cited by this record.")
			return
		}
		result.Commit = chosen
		object, err = s.workspace.Store.Preview(ctx, config.ObjectFormat, chosen, input.Path)
	default:
		object, err = s.workspace.Store.Preview(ctx, config.ObjectFormat, eventCommit, "attachments")
		if errors.Is(err, gitstore.ErrPreviewMissing) {
			write(writer, result, nil)
			return
		}
		if err == nil && object.Directory {
			result.Attachments = object.Entries
			result.Omitted = object.Omitted
			write(writer, result, nil)
			return
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, gitstore.ErrPreviewMissing):
			fail("missing", "The file is absent at this exact revision.")
		case errors.Is(err, gitstore.ErrPreviewLimit):
			fail("oversize", "This file or its Git metadata exceeds the bounded preview limit.")
		case errors.Is(err, gitstore.ErrPreviewType):
			fail("unsupported", "Symbolic links and submodules are not previewed.")
		default:
			fail("unavailable", "The exact Git object could not be read and verified.")
		}
		return
	}
	if object.Directory {
		result.Status = "directory"
		result.Entries = object.Entries
		result.Omitted = object.Omitted
		write(writer, result, nil)
		return
	}
	if !utf8.Valid(object.Content) || bytes.IndexByte(object.Content, 0) >= 0 {
		fail("binary", "This file is not readable UTF-8 text.")
		return
	}
	if bytes.Count(object.Content, []byte{'\n'}) >= 5000 {
		fail("oversize", "This file exceeds the 5,000-line preview limit.")
		return
	}
	result.Content = string(object.Content)
	write(writer, result, nil)
}

// An exact head on the chosen record wins. Otherwise use only its directly
// cited artifacts: no current-main fallback or unbounded provenance walk.
func previewHeads(p workroom.Projection, event, format string) []string {
	unique := map[string]bool{}
	add := func(head string) {
		size := 40
		if format == "sha256" {
			size = 64
		}
		if len(head) != size {
			return
		}
		for _, r := range head {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
				return
			}
		}
		unique[head] = true
	}
	for _, a := range p.Artifacts {
		if a.Event == event {
			add(a.Commit)
		}
	}
	for _, r := range p.Reviews {
		if r.Report == event {
			add(r.Head)
		}
	}
	for _, statement := range p.Statements {
		if statement.Event == event {
			add(statement.Body["head"])
			add(statement.Body["commit"])
			break
		}
	}
	if len(unique) == 0 {
		bases := map[string]bool{}
		for _, basis := range p.Provenance[event] {
			bases[basis] = true
		}
		for _, a := range p.Artifacts {
			if bases[a.Event] {
				add(a.Commit)
			}
		}
	}
	heads := make([]string, 0, len(unique))
	for head := range unique {
		heads = append(heads, head)
	}
	sort.Strings(heads)
	return heads
}

func decodePreview(request *http.Request, input *previewRequest) error {
	if err := guardMutation(request); err != nil {
		return err
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one preview request")
	}
	return nil
}
