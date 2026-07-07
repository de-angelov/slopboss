package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DevAgentCount is the number of dev-agent lanes the loop manages. It is seeded
// from CONFIG.md (falling back to DefaultDevAgents); the run command then derives
// the authoritative value from the repo-agent-* workspaces at startup via
// DiscoverDevAgentCount.
var DevAgentCount = loadDevAgents()

const (
	DevAgentRolePrefix = "Dev Agent "
	InProgressSuffix   = " In Progress"
)

func DevAgentRole(index int) string    { return fmt.Sprintf("%s%d", DevAgentRolePrefix, index) }
func DevAgentSection(index int) string { return DevAgentRole(index) + InProgressSuffix }

func DevAgentWorkspace(index int) string {
	return filepath.Join(WorkspacesRoot, fmt.Sprintf("repo-agent-%d", index))
}

// DevAgentIndexForRole parses "Dev Agent K" into K. ok is false for non-dev
// roles or malformed indices.
func DevAgentIndexForRole(role string) (int, bool) {
	if !strings.HasPrefix(role, DevAgentRolePrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(role, DevAgentRolePrefix)))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// DevAgentRoleForActiveSection maps a dev-agent board section to its role, e.g.
// "Dev Agent 2 In Progress" -> "Dev Agent 2", for indices within the configured
// agent count. The trailing " In Progress" is tolerated but optional: a Team
// Lead grooming pass that rewrites the header as plain "Dev Agent 2" must not
// silently stop the lane from being scheduled. Task status is enforced
// separately by the caller.
func DevAgentRoleForActiveSection(section string) (string, bool) {
	role := strings.TrimSuffix(section, InProgressSuffix)
	idx, ok := DevAgentIndexForRole(role)
	if !ok || idx > DevAgentCount {
		return "", false
	}
	return role, true
}

// DiscoverDevAgentCount counts the contiguous repo-agent-N workspaces
// (repo-agent-1, repo-agent-2, ...), stopping at the first missing index.
func DiscoverDevAgentCount() int {
	count := 0
	for i := 1; ; i++ {
		info, err := os.Stat(DevAgentWorkspace(i))
		if err != nil || !info.IsDir() {
			break
		}
		count++
	}
	return count
}
