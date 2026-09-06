package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	PreviewContentLimit = 512 << 10
	previewTreeBudget   = 4 << 20
	PreviewEntryLimit   = 128
)

var (
	ErrPreviewMissing = errors.New("path is not present at this revision")
	ErrPreviewLimit   = errors.New("content exceeds the preview limit")
	ErrPreviewType    = errors.New("only regular files and directories can be previewed")
)

type PreviewObject struct {
	Content   []byte
	Entries   []string
	Omitted   int
	Directory bool
}

// ValidPreviewPath accepts literal repository paths, never a revision
// expression, normalized traversal, host path or platform-dependent separator.
func ValidPreviewPath(path string) bool {
	if path == "" || len(path) > 2048 || !utf8.ValidString(path) || strings.ContainsAny(path, "\\:\x00\r\n\t") {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) > 32 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// Preview reads only hash-verified immutable Git objects from this store. It
// does not follow symlinks, filters, worktree files or replacement objects.
// One request has a 1MiB commit, 4MiB aggregate tree and 512KiB content budget.
func (s Store) Preview(ctx context.Context, format, commit, path string) (result PreviewObject, err error) {
	if !ValidPreviewPath(path) {
		return result, errors.New("invalid repository path")
	}
	batch, err := s.OpenAuditBatch(ctx, format)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, batch.Close()) }()
	raw, err := batch.readObject(commit, "commit", 1<<20)
	if err != nil {
		return result, previewReadError(err)
	}
	headers, _, ok := bytes.Cut(raw, []byte("\n\n"))
	if !ok {
		return result, errors.New("invalid commit")
	}
	tree := ""
	for _, line := range bytes.Split(headers, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("tree ")) {
			if tree != "" {
				return result, errors.New("duplicate commit tree")
			}
			tree = string(line[5:])
		}
	}
	if !validObjectID(tree, batch.objectBytes) {
		return result, errors.New("invalid commit tree")
	}
	budget := uint64(previewTreeBudget)
	parts := strings.Split(path, "/")
	for i, part := range parts {
		entries, err := batch.previewTree(tree, &budget)
		if err != nil {
			return result, err
		}
		var found *auditedTreeEntry
		for j := range entries {
			if entries[j].name == part {
				if found != nil {
					return result, errors.New("ambiguous tree path")
				}
				found = &entries[j]
			}
		}
		if found == nil {
			return result, ErrPreviewMissing
		}
		if i < len(parts)-1 {
			if found.mode != "40000" {
				return result, ErrPreviewType
			}
			tree = found.oid
			continue
		}
		switch found.mode {
		case "40000":
			children, err := batch.previewTree(found.oid, &budget)
			if err != nil {
				return result, err
			}
			result.Directory = true
			for _, child := range children {
				if !utf8.ValidString(child.name) {
					return result, errors.New("invalid tree name")
				}
				if len(result.Entries) < PreviewEntryLimit {
					result.Entries = append(result.Entries, child.name)
				} else {
					result.Omitted++
				}
			}
			return result, nil
		case "100644", "100755":
			result.Content, err = batch.readObject(found.oid, "blob", PreviewContentLimit)
			return result, previewReadError(err)
		default:
			return result, ErrPreviewType
		}
	}
	return result, ErrPreviewMissing
}

func previewReadError(err error) error {
	if err != nil && strings.Contains(err.Error(), "exceeds limit") {
		return ErrPreviewLimit
	}
	return err
}

func (b *AuditBatch) previewTree(oid string, budget *uint64) ([]auditedTreeEntry, error) {
	content, err := b.readObject(oid, "tree", *budget)
	if err != nil {
		return nil, previewReadError(err)
	}
	*budget -= uint64(len(content))
	var entries []auditedTreeEntry
	for len(content) > 0 {
		space, nul := bytes.IndexByte(content, ' '), bytes.IndexByte(content, 0)
		if space <= 0 || nul <= space+1 || len(content) < nul+1+b.objectBytes {
			return nil, errors.New("malformed git tree")
		}
		entries = append(entries, auditedTreeEntry{mode: string(content[:space]), name: string(content[space+1 : nul]), oid: fmt.Sprintf("%x", content[nul+1:nul+1+b.objectBytes])})
		content = content[nul+1+b.objectBytes:]
	}
	return entries, nil
}
