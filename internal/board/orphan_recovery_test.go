package board

import "testing"

func TestOwnedBlockedChainDoesNotOverrideReadyWork(t *testing.T) {
	tasks := []Task{
		{ID: "DOG-04", Section: "Blocked Tasks", Status: "Blocked", Category: "AFK", Dependencies: "DOG-15"},
		{ID: "DOG-15", Section: "Blocked Tasks", Status: "Blocked", Category: "AFK", Dependencies: "DOG-16"},
		{ID: "DOG-16", Section: "Backlog", Status: "Backlog", Category: "AFK", Title: "repair archive"},
	}
	if HasOrphanBlocker(tasks) {
		t.Fatal("blocked work with linked owners was classified as orphaned")
	}
}

func TestUnownedBlockedWorkRemainsOrphaned(t *testing.T) {
	for _, deps := range []string{"", "none", "missing", "DOG-04"} {
		tasks := []Task{{ID: "DOG-04", Section: "Blocked Tasks", Status: "Blocked", Category: "AFK", Dependencies: deps}}
		if !HasOrphanBlocker(tasks) {
			t.Fatalf("dependencies %q should not establish an owning task", deps)
		}
	}
}
