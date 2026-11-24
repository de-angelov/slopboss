package board

import (
	"os"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestTaskBodyRejectsUnusableVerificationAtCreation(t *testing.T) {
	body := "Objective: Investigate. Scope: Probe cases. Out of Scope: Changes. Acceptance Criteria: Report. Verification: npx vitest run tests/pipeline.test.ts --reporter=basic --no-color; inspect report reproduction steps."
	if err := validateTaskBody(body); err == nil || !strings.Contains(strings.ToLower(err.Error()), "verification") {
		t.Fatalf("expected actionable verification validation error, got %v", err)
	}
}

func TestCreateBacklogTaskRejectsEmbeddedVerificationWithoutWriting(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	before := "# BACKLOG\n\n## Backlog\n"
	writeBoard(t, config.BacklogFile, before)
	body := "Objective: Investigate. Scope: Probe. Out of Scope: Changes. Acceptance Criteria: Report. Verification: go test ./...; inspect the report."
	_, err := CreateBacklogTask(Task{ID: "HARD-05", Title: "Investigate", Category: "AFK", Body: body})
	if err == nil || !strings.Contains(err.Error(), "Verification") {
		t.Fatalf("CreateBacklogTask error = %v, want actionable verification error", err)
	}
	after, err := os.ReadFile(config.BacklogFile)
	if err != nil || string(after) != before {
		t.Fatalf("backlog changed on rejected task: %q, %v", after, err)
	}
}

func TestTaskBodyVerificationFormats(t *testing.T) {
	prefix := "Objective: Investigate.\nScope: Probe cases.\nOut of Scope: Changes.\nAcceptance Criteria: Report.\n"
	for _, tc := range []struct {
		name, verification string
		valid              bool
	}{
		{"inline", "Verification: go test ./internal/board", true},
		{"fenced", "## Verification\n```sh\ngo test ./internal/board\n```\nInspect the report separately.", true},
		{"empty", "Verification:", false},
		{"ambiguous prose", "Verification: go test ./internal/board. Inspect the report.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTaskBody(prefix + tc.verification)
			if (err == nil) != tc.valid {
				t.Fatalf("validateTaskBody error = %v, valid = %v", err, tc.valid)
			}
		})
	}
}
