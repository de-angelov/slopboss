package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

const agentLeadSection = "Team Lead Coordination"
const agentLeadPrefix = "coordinate\x00"

// coordinationSnapshot excludes volatile heartbeat timestamps and the Team
// Lead's own lease. A new worker result or board edit creates a new wakeup.
type coordinationTask struct {
	ID, Title, Section, Owner, Status, Category, Branch, Dependencies, BlockingTasks, BodyHash string
}
type coordinationWorker struct {
	Role, TaskID, Branch, LeaseID, Outcome, Evidence string
	Attempt                                          int
}
type coordinationSnapshot struct {
	Tasks          []coordinationTask   `json:"tasks"`
	Completed      []string             `json:"completed_task_ids"`
	Workers        []coordinationWorker `json:"worker_leases"`
	AvailableRoles []string             `json:"configured_worker_roles"`
}

func coordinationAction(tasks []board.Task, done map[string]bool, durable ControllerState) desiredSession {
	snapshot := coordinationSnapshot{Tasks: []coordinationTask{}, Completed: []string{}, Workers: []coordinationWorker{}, AvailableRoles: []string{}}
	for _, task := range tasks {
		body := sha256.Sum256([]byte(task.Body))
		snapshot.Tasks = append(snapshot.Tasks, coordinationTask{task.ID, task.Title, task.Section, task.Owner, task.Status, task.Category, task.Branch, task.Dependencies, task.BlockingTasks, hex.EncodeToString(body[:])})
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool {
		a, _ := json.Marshal(snapshot.Tasks[i])
		b, _ := json.Marshal(snapshot.Tasks[j])
		return string(a) < string(b)
	})
	for id, complete := range done {
		if complete {
			snapshot.Completed = append(snapshot.Completed, id)
		}
	}
	sort.Strings(snapshot.Completed)
	for _, lease := range durable.Leases {
		if _, ok := config.DevAgentIndexForRole(lease.Intent.Role); !ok {
			continue
		}
		worker := coordinationWorker{Role: lease.Intent.Role, TaskID: lease.Intent.TaskID, Branch: lease.Intent.Branch, LeaseID: lease.LeaseID, Attempt: lease.Attempt, Outcome: "unresolved"}
		if lease.Terminal != nil {
			worker.Outcome, worker.Evidence = lease.Terminal.Outcome, lease.Terminal.Evidence
		}
		snapshot.Workers = append(snapshot.Workers, worker)
	}
	sort.Slice(snapshot.Workers, func(i, j int) bool { return snapshot.Workers[i].Role < snapshot.Workers[j].Role })
	for i := 1; i <= config.DevAgentCount; i++ {
		snapshot.AvailableRoles = append(snapshot.AvailableRoles, config.DevAgentRole(i))
	}
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	key := agentLeadPrefix + hex.EncodeToString(sum[:])
	body := fmt.Sprintf(`Choose the most useful next coordination action using the snapshot below and the board tools. Review completed or failed worker sessions, assign ready work, investigate blockers, or make no changes if the board needs no action. A worker's process result alone does not prove its work was merged. Inspect the relevant task and evidence before changing its state. Use slopboss --dir <board-root> board assign TASK-ID "Dev Agent N" for assignments. If a finished worker must continue the same task, use slopboss --dir <board-root> board resume TASK-ID --reason "specific next step". Use structural slopboss board commands for other board updates; do not edit board files directly. Do not make product-code changes. If a worker is unresolved, do not assume it stopped or reassign its work. Explain decisions and any uncertainty in your final response.

The snapshot records the last durable worker leases, not a guarantee of current process liveness. Task body hashes detect changes; use slopboss board get TASK-ID to inspect a task's full body.

Snapshot:
%s`, string(raw))
	task := board.Task{Section: agentLeadSection, Title: "Coordinate the board", Status: "In Progress", Body: body, Key: key}
	return desiredSession{Task: task, TaskKey: key}
}

func isAgentLeadTask(task board.Task) bool {
	return task.Section == agentLeadSection && strings.HasPrefix(task.Key, agentLeadPrefix)
}
