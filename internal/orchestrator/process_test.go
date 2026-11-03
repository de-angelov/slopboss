package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestLeaseInspectionTracksRecordedProcessWithoutClaimingSafety(t *testing.T) {
	if _, err := os.Stat("/proc/sys/kernel/random/boot_id"); err != nil {
		t.Skip("process identity requires procfs")
	}
	oldRoot, oldStore := config.WorkspacesRoot, controllerStateStore
	config.WorkspacesRoot = t.TempDir()
	controllerStateStore = NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	t.Cleanup(func() { config.WorkspacesRoot, controllerStateStore = oldRoot, oldStore })
	now := time.Now()
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "A", Role: devAgent1Role, Branch: "branch"}, LeaseID: "lease", HeartbeatAt: now, Attempt: 1}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := recordWorkerProcess("lease", cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectWorkerLease(devAgent1Role)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Observation != "process-present" || inspection.Process.PID != cmd.Process.Pid || !strings.Contains(inspection.Note, "does not establish") {
		t.Fatalf("inspection: %#v", inspection)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	inspection, err = InspectWorkerLease(devAgent1Role)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Observation != "not-observed" {
		t.Fatalf("after stop: %#v", inspection)
	}
}
