package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/de-angelov/slopboss/internal/config"
)

type processLeaseKey struct{}

func processIdentity(pid int) ControllerProcess {
	identity := ControllerProcess{PID: pid}
	if group, err := syscall.Getpgid(pid); err == nil {
		identity.ProcessGroup = group
	}
	if boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		identity.BootID = strings.TrimSpace(string(boot))
	}
	identity.StartTicks, _ = processStartTicks(pid)
	return identity
}
func processStartTicks(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	// The command name is parenthesized and can itself contain spaces or ')'.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data)[end+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("incomplete process stat")
	}
	return fields[19], nil
}
func recordWorkerProcess(leaseID string, pid int) error {
	identity := processIdentity(pid)
	controllerMu.Lock()
	defer controllerMu.Unlock()
	state, err := loadControllerState()
	if err != nil {
		return err
	}
	for i := range state.Leases {
		lease := &state.Leases[i]
		if lease.LeaseID != leaseID {
			continue
		}
		if lease.Terminal != nil {
			return fmt.Errorf("lease is terminal")
		}
		lease.Process = &identity
		return commitControllerState(state)
	}
	return fmt.Errorf("lease %q not found", leaseID)
}

type LeaseInspection struct {
	Role        string             `json:"role"`
	TaskID      string             `json:"task_id"`
	LeaseID     string             `json:"lease_id"`
	HeartbeatAt string             `json:"heartbeat_at"`
	Outcome     string             `json:"outcome"`
	Evidence    string             `json:"evidence,omitempty"`
	Process     *ControllerProcess `json:"process,omitempty"`
	Observation string             `json:"observation"`
	Note        string             `json:"note"`
}

// InspectWorkerLease supplies diagnostic evidence only. A missing or changed
// PID never proves descendants or remote workers have stopped.
func InspectWorkerLease(role string) (LeaseInspection, error) {
	store := NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	state, err := store.Load()
	if err != nil {
		return LeaseInspection{}, err
	}
	lease, found := FindControllerLeaseByTarget(state, role)
	if !found {
		return LeaseInspection{}, fmt.Errorf("no lease found for %q", role)
	}
	out := LeaseInspection{Role: lease.Intent.Role, TaskID: lease.Intent.TaskID, LeaseID: lease.LeaseID, HeartbeatAt: lease.HeartbeatAt.Format("2006-01-02T15:04:05Z07:00"), Outcome: "unresolved", Process: lease.Process, Observation: "unknown", Note: "This observation does not establish whether descendants or remote workers have stopped."}
	if lease.Terminal != nil {
		out.Outcome = lease.Terminal.Outcome
		out.Evidence = lease.Terminal.Evidence
	}
	p := lease.Process
	if p == nil || p.BootID == "" || p.StartTicks == "" {
		return out, nil
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return out, nil
	}
	if strings.TrimSpace(string(boot)) != p.BootID {
		out.Observation = "different-boot"
		return out, nil
	}
	ticks, err := processStartTicks(p.PID)
	if os.IsNotExist(err) {
		out.Observation = "not-observed"
		return out, nil
	}
	if err != nil {
		return out, nil
	}
	if ticks != p.StartTicks {
		out.Observation = "pid-reused"
	} else {
		out.Observation = "process-present"
	}
	return out, nil
}
