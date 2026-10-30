package board

import (
	"fmt"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
)

// LifecycleState is the machine-owned state of a task. It is derived from the
// task's location and metadata; agents never write it as an extra field.
type LifecycleState string

const (
	StateBacklog    LifecycleState = "backlog"
	StateInProgress LifecycleState = "in_progress"
	StateBlocked    LifecycleState = "blocked"
	StateDone       LifecycleState = "done"
)

// ValidateLifecycle is the lifecycle module's interface. It accepts a fully
// materialized task and its board file, returning its state only when every
// location/owner/status invariant holds.
func ValidateLifecycle(task Task, file string) (LifecycleState, error) {
	file = strings.ToLower(strings.TrimSpace(file))
	switch {
	case file == "backlog" && task.Section == "Backlog" && task.Status == "Backlog" && task.Owner == "Unassigned":
		return StateBacklog, nil
	case file == "tasks" && task.Section == "Blocked Tasks" && task.Status == "Blocked" && task.Owner == "Unassigned":
		return StateBlocked, nil
	case file == "archive" && task.Status == "Done":
		return StateDone, nil
	case file == "tasks" && task.Status == "In Progress":
		if role, ok := config.DevAgentRoleForActiveSection(task.Section); ok && task.Owner == role {
			return StateInProgress, nil
		}
	}
	return "", fmt.Errorf("invalid lifecycle: file=%q section=%q owner=%q status=%q", file, task.Section, task.Owner, task.Status)
}

func boardFileName(path string) string {
	switch path {
	case config.BacklogFile:
		return "backlog"
	case config.TasksFile:
		return "tasks"
	case config.ArchiveFile:
		return "archive"
	default:
		return ""
	}
}
