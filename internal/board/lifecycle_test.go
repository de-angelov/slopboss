package board

import (
	"testing"
)

func TestValidateLifecycle(t *testing.T) {
	cases := []struct {
		name string
		task Task
		file string
		want LifecycleState
		ok   bool
	}{
		{"backlog", Task{Section: "Backlog", Owner: "Unassigned", Status: "Backlog"}, "backlog", StateBacklog, true},
		{"active", Task{Section: "Dev Agent 1 In Progress", Owner: "Dev Agent 1", Status: "In Progress"}, "tasks", StateInProgress, true},
		{"blocked", Task{Section: "Blocked Tasks", Owner: "Unassigned", Status: "Blocked"}, "tasks", StateBlocked, true},
		{"done", Task{Section: "Done", Owner: "Unassigned", Status: "Done"}, "archive", StateDone, true},
		{"blocked assigned to lane", Task{Section: "Dev Agent 1 In Progress", Owner: "Dev Agent 1", Status: "Blocked"}, "tasks", "", false},
	}
	for _, tc := range cases {
		got, err := ValidateLifecycle(tc.task, tc.file)
		if (err == nil) != tc.ok || got != tc.want {
			t.Fatalf("%s: state=%q err=%v", tc.name, got, err)
		}
	}
}
