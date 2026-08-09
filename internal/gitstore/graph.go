package gitstore

import (
	"context"
	"strconv"
	"strings"
)

// GraphCommit is one row of the repo-underneath railway: an ordinary git
// commit with its parents, decorations, and any Rests-On trailers that
// bridge it to workroom events.
type GraphCommit struct {
	Hash    string   `json:"hash"`
	Parents []string `json:"parents"`
	Refs    []string `json:"refs,omitempty"`
	Subject string   `json:"subject"`
	Author  string   `json:"author"`
	Time    int64    `json:"time"`
	Body    string   `json:"body,omitempty"`
	RestsOn []string `json:"rests_on,omitempty"`
}

// Graph lists recent commits across branches and tags in topological order,
// newest first. It deliberately excludes refs/seq/* — the sequenced log is
// the agreed-sequence pane, not a railway lane.
func (s Store) Graph(ctx context.Context, limit int) ([]GraphCommit, error) {
	if limit <= 0 {
		limit = 60
	}
	format := "%H%x1f%P%x1f%D%x1f%s%x1f%an%x1f%at%x1f%(trailers:key=Rests-On,valueonly=true)%x1f%b%x1e"
	output, err := s.run(ctx, nil, nil,
		"log", "--topo-order", "-n", strconv.Itoa(limit),
		"--branches", "--tags", "--format="+format)
	if err != nil {
		// An unborn repository (no commits yet) renders an empty railway.
		if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "bad revision") {
			return nil, nil
		}
		return nil, err
	}
	var commits []GraphCommit
	for _, record := range strings.Split(string(output), "\x1e") {
		record = strings.TrimLeft(record, "\n")
		if strings.TrimSpace(record) == "" {
			continue
		}
		fields := strings.Split(record, "\x1f")
		if len(fields) < 8 {
			continue
		}
		commit := GraphCommit{Hash: fields[0], Subject: fields[3], Author: fields[4]}
		if fields[1] != "" {
			commit.Parents = strings.Fields(fields[1])
		}
		for _, ref := range strings.Split(fields[2], ", ") {
			ref = strings.TrimSpace(strings.TrimPrefix(ref, "HEAD -> "))
			if ref != "" && ref != "HEAD" && !strings.HasPrefix(ref, "refs/seq/") {
				commits := strings.TrimPrefix(strings.TrimPrefix(ref, "tag: "), "refs/heads/")
				commit.Refs = append(commit.Refs, commits)
			}
		}
		if seconds, err := strconv.ParseInt(fields[5], 10, 64); err == nil {
			commit.Time = seconds
		}
		for _, trailer := range strings.Split(fields[6], "\n") {
			if trailer = strings.TrimSpace(trailer); trailer != "" {
				commit.RestsOn = append(commit.RestsOn, trailer)
			}
		}
		commit.Body = strings.TrimSpace(fields[7])
		commits = append(commits, commit)
	}
	return commits, nil
}
