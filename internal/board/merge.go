package board

import (
	"regexp"
	"strings"
	"sync"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
)

// mergeScanDepth bounds how many recent origin/main commits detectMergedMainIDs
// scans for task-ID-prefixed squash commits. ARCHIVE.md holds only dozens of
// done tasks, so a few hundred commits comfortably covers the project's history
// while keeping the read cheap.
const mergeScanDepth = 400

// taskIDPrefixRe matches the task-ID token that leads a squash-merge commit
// subject on main, e.g. "BOARD-03B", "TICKET-08", "BOARD-PERF-01A". Dev agents
// squash each completed task into main as one commit whose subject begins with
// the task ID (see DEV_AGENT.md), so a subject's leading token identifies the
// task whose work has landed. At least one hyphen segment is required so plain
// words ("WIP", "Merge") never match.
var taskIDPrefixRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+`)

var (
	mergedMainIDs = map[string]bool{}
	mergedMainMu  sync.Mutex
)

// mergedIDFromSubject returns the leading task ID of a main commit subject, or ""
// if the subject does not begin with a task-ID token.
func mergedIDFromSubject(subject string) string {
	return taskIDPrefixRe.FindString(strings.TrimSpace(subject))
}

// detectMergedMainIDs returns the set of task IDs whose squash-merge commit has
// landed on origin/main. This is the orchestrator's git-side reconciliation
// signal: a task can be fully merged yet never recorded Done in ARCHIVE.md (its
// dev agent was cancelled before the final archive step), which otherwise leaves
// every downstream dependency blocked forever. It reads the already-fetched local
// origin/main ref of the team-lead workspace — dev agents push there and the team
// lead fetches at every session start, so the ref stays fresh without this pass
// doing any network I/O of its own.
func detectMergedMainIDs() map[string]bool {
	merged := map[string]bool{}
	if !git.WorkspaceExists(config.TeamLeadRole) {
		return merged
	}
	for _, subject := range git.MainCommitSubjects(config.TeamLeadPath, mergeScanDepth) {
		if id := mergedIDFromSubject(subject); id != "" {
			merged[id] = true
		}
	}
	return merged
}

// RefreshMergedMainIDs recomputes the merged-to-main task-ID set and publishes it
// for CompletedSet to fold into dependency resolution. Called from the poll loop
// before each reconcile so newly-merged work unblocks its dependents on the next
// tick even when no agent recorded it Done.
func RefreshMergedMainIDs() {
	merged := detectMergedMainIDs()

	mergedMainMu.Lock()
	mergedMainIDs = merged
	mergedMainMu.Unlock()
}

// mergedMainIDsSnapshot returns a copy of the currently-published merged-to-main
// set. It locks only mergedMainMu (never any orchestrator mutex) so it is safe to
// call from paths that already hold other locks, such as the UI render path.
func mergedMainIDsSnapshot() map[string]bool {
	mergedMainMu.Lock()
	defer mergedMainMu.Unlock()

	snapshot := make(map[string]bool, len(mergedMainIDs))
	for id := range mergedMainIDs {
		snapshot[id] = true
	}
	return snapshot
}
