package board

import (
	"fmt"
	"os"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
)

// Assign moves an eligible task into an available worker lane under one board
// lock. Repeating an already completed assignment is safe.
func Assign(id, role string) (Task, error) {
	unlock, err := lockBoard()
	if err != nil {
		return Task{}, err
	}
	defer unlock()
	id, role = strings.TrimSpace(id), strings.TrimSpace(role)
	index, ok := config.DevAgentIndexForRole(role)
	if !ok || index > config.DevAgentCount || config.DevAgentRole(index) != role {
		return Task{}, fmt.Errorf("unknown configured worker %q", role)
	}
	path, source, err := findTaskSource(id)
	if err != nil {
		return Task{}, err
	}
	task := parseAllTasks("## "+sectionAt(source.data, source.start)+"\n"+source.block, path)[id]
	data, err := os.ReadFile(config.TasksFile)
	if err != nil {
		return Task{}, err
	}
	active := ActiveTasksForRole(ParseTasks(string(data)), role)
	if len(active) > 0 {
		if len(active) == 1 && active[0].ID == id && task.Owner == role && strings.EqualFold(task.Status, "In Progress") {
			return task, nil
		}
		return Task{}, fmt.Errorf("worker %q already has active work", role)
	}
	if strings.EqualFold(task.Category, "HITL") {
		return Task{}, fmt.Errorf("task %q requires human input", id)
	}
	if !(task.Section == "Backlog" && (task.Status == "" || strings.EqualFold(task.Status, "Backlog")) || task.Section == "Blocked Tasks" && strings.EqualFold(task.Status, "Blocked")) {
		return Task{}, fmt.Errorf("task %q is not available for assignment", id)
	}
	if !DependenciesSatisfied(task, CompletedSet()) {
		return Task{}, fmt.Errorf("task %q has unfinished dependencies", id)
	}
	return setUnlocked(id, Update{File: "tasks", Section: config.DevAgentSection(index), Owner: role, Status: "In Progress", ClearBlockedBy: true})
}
