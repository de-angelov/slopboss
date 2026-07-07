package board

import (
	"sort"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
)

// FindDesiredTaskForRole returns the task the given role should currently be
// working, or the zero Task when the role should be idle.
func FindDesiredTaskForRole(tasks []Task, role string) Task {
	if role == config.TeamLeadRole {
		if HasBoardError(tasks) || !LanesHaveCapacity(tasks) {
			return Task{}
		}
		return FirstBacklogTask(tasks)
	}
	if _, ok := config.DevAgentIndexForRole(role); ok {
		activeTasks := ActiveTasksForRole(tasks, role)
		if len(activeTasks) == 1 {
			return activeTasks[0]
		}
	}
	return Task{}
}

// ActiveTasksForRole returns the In Progress tasks a dev-agent role owns in its
// lane. More than one indicates a board error.
func ActiveTasksForRole(tasks []Task, role string) []Task {
	if _, ok := config.DevAgentIndexForRole(role); !ok {
		return nil
	}

	var active []Task
	for _, task := range tasks {
		if r, ok := config.DevAgentRoleForActiveSection(task.Section); ok && r == role &&
			task.Owner == role && task.Status == "In Progress" {
			active = append(active, task)
		}
	}
	return active
}

// HasBoardError reports whether any dev-agent lane has more than one active task.
func HasBoardError(tasks []Task) bool {
	for k := 1; k <= config.DevAgentCount; k++ {
		if len(ActiveTasksForRole(tasks, config.DevAgentRole(k))) > 1 {
			return true
		}
	}
	return false
}

// LanesHaveCapacity reports whether at least one dev-agent lane is empty.
func LanesHaveCapacity(tasks []Task) bool {
	for k := 1; k <= config.DevAgentCount; k++ {
		if len(ActiveTasksForRole(tasks, config.DevAgentRole(k))) == 0 {
			return true
		}
	}
	return false
}

// HasBacklog reports whether any assignable backlog task exists.
func HasBacklog(tasks []Task) bool {
	return FirstBacklogTask(tasks).Title != ""
}

// FirstBacklogTask returns the backlog task the team lead should be pointed at
// next: the highest-priority task that is actually assignable right now. A task
// is assignable only if every one of its dependencies is already completed —
// pointing the team lead at a task whose dependencies are unmet (or reference a
// phantom/never-authored task) just burns a grooming session on work no dev
// agent can start. Among assignable tasks, AFK ("away from keyboard",
// autonomously runnable) wins over HITL, and board order breaks further ties.
// Returns the zero Task when nothing is assignable, which leaves the team lead
// idle rather than spinning on blocked work.
func FirstBacklogTask(tasks []Task) Task {
	done := CompletedSet()

	var firstReady Task
	found := false
	for _, task := range tasks {
		if task.Section != "Backlog" || !(task.Status == "Backlog" || task.Status == "") {
			continue
		}
		if !DependenciesSatisfied(task, done) {
			continue
		}
		if strings.EqualFold(task.Category, "AFK") {
			return task
		}
		if !found {
			firstReady = task
			found = true
		}
	}
	return firstReady
}

// AllDevAgentsBusy reports whether every configured dev-agent lane already has
// desired work (so the Team Lead should not be handed a backlog task). With zero
// configured agents this is vacuously true.
func AllDevAgentsBusy(desired map[string]Task) bool {
	for k := 1; k <= config.DevAgentCount; k++ {
		if _, busy := desired[config.DevAgentRole(k)]; !busy {
			return false
		}
	}
	return true
}

// BlockedBacklogIDs returns the IDs of pending backlog tasks whose dependencies
// are not all completed, plus any task explicitly marked Status: Blocked. Sorted
// so it forms a stable key: a decomposition pass is throttled to run once per
// distinct set of blockers rather than on every poll. IDs fall back to the title
// when a task carries no Task ID.
func BlockedBacklogIDs(tasks []Task, done map[string]bool) []string {
	var ids []string
	for _, task := range tasks {
		pending := task.Section == "Backlog" && (task.Status == "Backlog" || task.Status == "")
		blocked := strings.EqualFold(task.Status, "Blocked")
		if (pending && !DependenciesSatisfied(task, done)) || blocked {
			id := task.ID
			if id == "" {
				id = task.Title
			}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// CompletedSet returns the task IDs that count as done for dependency
// resolution: everything recorded Done in ARCHIVE.md, unioned with work whose
// squash commit is already on origin/main. The union is what lets a
// merged-but-unarchived task unblock its dependents instead of stalling the
// pipeline behind a task that is, in fact, finished.
func CompletedSet() map[string]bool {
	done := map[string]bool{}
	for _, id := range completedTaskIDs() {
		done[id] = true
	}
	for id := range mergedMainIDsSnapshot() {
		done[id] = true
	}
	return done
}

// CompletedContextIDs returns the sorted union of task IDs recorded Done in
// ARCHIVE.md and IDs whose squash commit is already on origin/main. It matches
// the set CompletedSet uses for scheduling, so the team lead's board context
// reflects the same notion of "done" the loop reconciles against. Sorted for a
// stable, deterministic listing.
func CompletedContextIDs() []string {
	seen := map[string]bool{}
	for _, id := range completedTaskIDs() {
		seen[id] = true
	}
	for id := range mergedMainIDsSnapshot() {
		seen[id] = true
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// completedTaskIDs returns the Task IDs recorded in ARCHIVE.md. The board parser
// deliberately skips the archive's Done section (it is large and its bodies are
// not needed), which leaves the team lead unable to tell whether a dependency is
// done. Scanning only the "Task ID:" lines gives grooming that signal at a tiny
// fraction of the tokens a full archive read would cost.
func completedTaskIDs() []string {
	data, err := fileCache.Read(config.ArchiveFile)
	if err != nil {
		return nil
	}

	var ids []string
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Task ID:") {
			if id := strings.TrimSpace(strings.TrimPrefix(trimmed, "Task ID:")); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// DependenciesSatisfied reports whether every dependency of task is completed.
// An empty or "none" dependency list is trivially satisfied. Dependencies are a
// comma-separated list of task IDs (e.g. "ACTIVITY-01B, TICKET-06").
func DependenciesSatisfied(task Task, done map[string]bool) bool {
	deps := strings.TrimSpace(task.Dependencies)
	if deps == "" || strings.EqualFold(deps, "none") {
		return true
	}
	for _, dep := range strings.Split(deps, ",") {
		dep = strings.TrimSpace(dep)
		if dep == "" || strings.EqualFold(dep, "none") {
			continue
		}
		if !done[dep] {
			return false
		}
	}
	return true
}
