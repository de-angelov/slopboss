package tui

import (
	"fmt"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/orchestrator"
)

type agentRow struct {
	marker string
	role   string
	status string
	task   string
	branch string
}

// buildRows renders one dashboard row per agent lane from the current board plus
// a session-state snapshot.
func buildRows(tasks []board.Task, snap orchestrator.StateSnapshot) []agentRow {
	rows := []agentRow{{role: config.TeamLeadRole}}
	for k := 1; k <= config.DevAgentCount; k++ {
		rows = append(rows, agentRow{role: config.DevAgentRole(k)})
	}

	for i := range rows {
		if session, ok := snap.Running[rows[i].role]; ok {
			rows[i].marker = ">"
			rows[i].status = "running"
			rows[i].task = session.Task
			rows[i].branch = session.Branch
			continue
		}

		if _, isDevAgent := config.DevAgentIndexForRole(rows[i].role); isDevAgent {
			activeTasks := board.ActiveTasksForRole(tasks, rows[i].role)
			if len(activeTasks) > 1 {
				rows[i].status = "board error"
				rows[i].task = fmt.Sprintf("%d active tasks", len(activeTasks))
				continue
			}
		}

		task := board.FindDesiredTaskForRole(tasks, rows[i].role)
		if task.Title != "" {
			if finishedSession, ok := snap.Finished[rows[i].role]; ok && finishedSession.TaskKey == task.Key {
				rows[i].status = string(finishedSession.Outcome)
			} else {
				rows[i].status = task.Status
			}
			rows[i].task = task.Title
			rows[i].branch = task.Branch
			continue
		}

		if rows[i].role == config.TeamLeadRole && board.HasBoardError(tasks) {
			rows[i].status = "board error"
		} else if rows[i].role == config.TeamLeadRole && board.HasBacklog(tasks) {
			if board.LanesHaveCapacity(tasks) {
				rows[i].status = "backlog pending"
			} else {
				rows[i].status = "waiting for lane"
			}
		} else {
			rows[i].status = "idle"
		}
	}

	return rows
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}
