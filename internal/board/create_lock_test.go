package board

import (
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestCreateDoesNotReacquireBoardLock(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n")
	done := make(chan error, 1)
	go func() {
		_, err := CreateBacklogTask(Task{ID: "TEST-1", Title: "Test", Category: "AFK", Body: "Objective: x\nScope: x\nOut of Scope: x\nAcceptance Criteria: x\nVerification: x"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("board create deadlocked")
	}
}
