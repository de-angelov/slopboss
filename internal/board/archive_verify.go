package board

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
)

var (
	mainCommitClaimRe = regexp.MustCompile(`(?i)\bmain\b.*\bcommit\b.*\b([0-9a-f]{7,40})\b|\bcommit\b.*\b([0-9a-f]{7,40})\b.*\bmain\b`)
	archiveHeadingRe  = regexp.MustCompile(`^###\s+(.+)$`)

	mainContainsCommit = git.MainContainsCommit
)

var (
	archiveIssues   []ArchiveCompletionIssue
	archiveIssuesMu sync.Mutex
)

type archiveCompletion struct {
	id         string
	title      string
	mainCommit string
}

// ArchiveCompletionIssue reports an ARCHIVE.md task whose recorded main commit
// is not actually reachable from origin/main in the product repository.
type ArchiveCompletionIssue struct {
	TaskID string
	Title  string
	Commit string
}

func (i ArchiveCompletionIssue) Error() string {
	return fmt.Sprintf("%s archived as Done with main commit %s, but that commit is not on origin/%s", i.TaskID, i.Commit, config.BaseBranch)
}

// ArchiveCompletionIssues returns archive entries that claim a main commit that
// is not reachable from origin/main. These entries are excluded from CompletedSet
// so dependency scheduling cannot advance from a stale Done record.
func ArchiveCompletionIssues() []ArchiveCompletionIssue {
	archiveIssuesMu.Lock()
	defer archiveIssuesMu.Unlock()

	return append([]ArchiveCompletionIssue(nil), archiveIssues...)
}

// RefreshArchiveCompletionIssues recomputes archive/main mismatches from the
// product repository's already-fetched origin/main ref and publishes them for
// CompletedSet and the UI. The poll loop calls this after fetching; render paths
// only read the cached snapshot.
func RefreshArchiveCompletionIssues() []ArchiveCompletionIssue {
	issues := archiveCompletionIssues(archivedCompletions())

	archiveIssuesMu.Lock()
	archiveIssues = issues
	archiveIssuesMu.Unlock()

	return append([]ArchiveCompletionIssue(nil), issues...)
}

func archiveCompletionIssues(completions []archiveCompletion) []ArchiveCompletionIssue {
	if !git.WorkspaceExists(config.TeamLeadRole) {
		return nil
	}

	var issues []ArchiveCompletionIssue
	for _, completion := range completions {
		if completion.id == "" || completion.mainCommit == "" {
			continue
		}
		if !mainContainsCommit(config.TeamLeadPath, completion.mainCommit) {
			issues = append(issues, ArchiveCompletionIssue{
				TaskID: completion.id,
				Title:  completion.title,
				Commit: completion.mainCommit,
			})
		}
	}
	return issues
}

func archivedCompletions() []archiveCompletion {
	data, err := fileCache.Read(config.ArchiveFile)
	if err != nil {
		return nil
	}

	var completions []archiveCompletion
	var current archiveCompletion
	flush := func() {
		if current.id != "" {
			completions = append(completions, current)
		}
		current = archiveCompletion{}
	}

	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if match := archiveHeadingRe.FindStringSubmatch(trimmed); match != nil {
			flush()
			current.title = strings.TrimSpace(match[1])
			continue
		}
		if strings.HasPrefix(trimmed, "Task ID:") {
			current.id = strings.TrimSpace(strings.TrimPrefix(trimmed, "Task ID:"))
			continue
		}
		if commit := mainCommitClaim(trimmed); commit != "" {
			current.mainCommit = commit
		}
	}
	flush()
	return completions
}

func mainCommitClaim(line string) string {
	match := mainCommitClaimRe.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	for _, group := range match[1:] {
		if group != "" {
			return group
		}
	}
	return ""
}
