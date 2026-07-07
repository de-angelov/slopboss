// Package config holds the process-wide paths, board-file locations, role
// identities, and tuning constants shared by every other package. It sits at the
// bottom of the dependency graph and imports nothing from the rest of slopboss.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultDevAgents is the dev-agent count used when none is configured.
const DefaultDevAgents = 2

// Timing and policy constants for the orchestrator loop.
const (
	PollInterval      = 10 * time.Second
	UIRefreshInterval = 500 * time.Millisecond

	// TeamLeadMaxTurns hard-caps the agentic turns of a team-lead grooming
	// session. Healthy grooming passes finish in ~5-9 turns; runaway
	// reconciliation sessions have hit 38+, and because every turn re-sends the
	// whole (large) transcript, token totals balloon superlinearly with turn
	// count. The cap is a backstop only — the prompt constraints do the primary
	// steering. A session that hits the cap simply resumes from the updated board
	// on the next poll, so capping never loses work. 0 (dev agents) means
	// unlimited.
	TeamLeadMaxTurns        = 20
	FailedSessionRetryDelay = time.Minute
	MaxFailedSessionRetries = 3
	CodexUsageLimitCooldown = 6 * time.Hour
	ShutdownGracePeriod     = 15 * time.Second

	// CancelGracePeriod is how long a running session may stay mismatched with
	// the board before reconcile cancels it. A dev agent rewrites
	// TASKS.md/ARCHIVE.md as the final step of completing its task, and the team
	// lead rewrites the board every grooming pass; cancelling on the first poll
	// that sees a mismatch kills sessions mid-completion and thrashes live work.
	// The grace period lets a transient mismatch resolve (or a completion finish)
	// before pulling the plug.
	CancelGracePeriod = 30 * time.Second

	DevAgentModel = "gpt-5.5"

	// DefaultProviderName selects the agent backend; codex is the default and
	// --provider switches it.
	DefaultProviderName = "codex"
	ClaudeDevModel      = "claude-sonnet-5"
)

// Role identities used across scheduling, prompting, and the UI.
const (
	TeamLeadRole  = "Team Lead Agent"
	DevAgent1Role = "Dev Agent 1"
	DevAgent2Role = "Dev Agent 2"
)

// repoRootMarkers are the board/config files that mark a directory as the repo
// root. All must be present for a directory to qualify.
var repoRootMarkers = []string{
	"BACKLOG.md",
	"TASKS.md",
	"ARCHIVE.md",
	"AGENTS.md",
	"DEV_AGENT.md",
	"TEAM_LEAD_AGENT.md",
	"TECH.md",
}

// Resolved paths. RepoRoot is discovered at startup; the rest hang off it.
var (
	RepoRoot       = MustResolveRepoRoot()
	WorkspacesRoot = filepath.Join(RepoRoot, "workspaces")
	LogsRoot       = filepath.Join(WorkspacesRoot, "logs")
	LogFilePath    = DefaultLogFilePath()

	BacklogFile              = filepath.Join(RepoRoot, "BACKLOG.md")
	TasksFile                = filepath.Join(RepoRoot, "TASKS.md")
	ArchiveFile              = filepath.Join(RepoRoot, "ARCHIVE.md")
	AgentsFile               = filepath.Join(RepoRoot, "AGENTS.md")
	DevAgentInstructionsFile = filepath.Join(RepoRoot, "DEV_AGENT.md")
	TlAgentInstructionsFile  = filepath.Join(RepoRoot, "TEAM_LEAD_AGENT.md")
	TechFile                 = filepath.Join(RepoRoot, "TECH.md")

	TeamLeadPath = filepath.Join(WorkspacesRoot, "repo-tl")
	Agent1Path   = filepath.Join(WorkspacesRoot, "repo-agent-1")
	Agent2Path   = filepath.Join(WorkspacesRoot, "repo-agent-2")

	// BaseBranch is the product repository's integration branch that dev agents
	// branch from and squash-merge into, and that merge detection scans. It is
	// chosen at setup and persisted in CONFIG.md; it defaults to "main".
	BaseBranch = loadBaseBranch()

	// Provider is the persisted default agent backend (codex/claude) chosen at
	// setup; commands use it as their --provider default. Defaults to
	// DefaultProviderName.
	Provider = loadProvider()

	// RepoURL is the product repository recorded at setup (for reference and as
	// the setup wizard's default on re-run). Empty until setup runs.
	RepoURL = settingOr("product repo", "")
)

// Settings is slopboss's persisted, per-board configuration. slopboss keeps all
// its state in Markdown (never JSON), so it is stored in CONFIG.md as
// "- Key: Value" bullets — the same convention as the board and experiment files.
type Settings struct {
	RepoURL    string
	BaseBranch string
	Provider   string
	DevAgents  int
}

// ConfigFilePath is the on-disk location of the persisted Markdown config.
func ConfigFilePath() string {
	return filepath.Join(RepoRoot, "CONFIG.md")
}

// SetRoot repoints RepoRoot and every path derived from it, then reloads the
// persisted settings from the new location. The setup wizard uses it so it can
// scaffold a board directory the user chooses instead of the current directory.
func SetRoot(root string) {
	RepoRoot = root
	WorkspacesRoot = filepath.Join(RepoRoot, "workspaces")
	LogsRoot = filepath.Join(WorkspacesRoot, "logs")
	LogFilePath = DefaultLogFilePath()

	BacklogFile = filepath.Join(RepoRoot, "BACKLOG.md")
	TasksFile = filepath.Join(RepoRoot, "TASKS.md")
	ArchiveFile = filepath.Join(RepoRoot, "ARCHIVE.md")
	AgentsFile = filepath.Join(RepoRoot, "AGENTS.md")
	DevAgentInstructionsFile = filepath.Join(RepoRoot, "DEV_AGENT.md")
	TlAgentInstructionsFile = filepath.Join(RepoRoot, "TEAM_LEAD_AGENT.md")
	TechFile = filepath.Join(RepoRoot, "TECH.md")

	TeamLeadPath = filepath.Join(WorkspacesRoot, "repo-tl")
	Agent1Path = filepath.Join(WorkspacesRoot, "repo-agent-1")
	Agent2Path = filepath.Join(WorkspacesRoot, "repo-agent-2")

	BaseBranch = loadBaseBranch()
	Provider = loadProvider()
	RepoURL = settingOr("product repo", "")
	DevAgentCount = loadDevAgents()
}

// settingOr returns a "- Key: Value" setting from CONFIG.md, or def if the file
// or key is missing.
func settingOr(key, def string) string {
	data, err := os.ReadFile(ConfigFilePath())
	if err != nil {
		return def
	}
	if v := mdSetting(string(data), key); v != "" {
		return v
	}
	return def
}

func loadBaseBranch() string { return settingOr("base branch", "main") }
func loadProvider() string   { return settingOr("provider", DefaultProviderName) }

func loadDevAgents() int {
	if n, err := strconv.Atoi(settingOr("dev agents", "")); err == nil && n >= 1 {
		return n
	}
	return DefaultDevAgents
}

// mdSetting extracts a "- Key: Value" bullet (case-insensitive key) from Markdown
// config content, ignoring prose and headings.
func mdSetting(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SaveSettings writes CONFIG.md and updates the in-process value so the current
// process reflects the change immediately.
func SaveSettings(s Settings) error {
	if strings.TrimSpace(s.BaseBranch) == "" {
		s.BaseBranch = "main"
	}
	if strings.TrimSpace(s.Provider) == "" {
		s.Provider = DefaultProviderName
	}
	if s.DevAgents < 1 {
		s.DevAgents = DefaultDevAgents
	}
	content := fmt.Sprintf(`# CONFIG

slopboss configuration for this board. Edit the "- Key: Value" settings below;
slopboss reads them on startup. This is the single home for slopboss config.

- Product repo: %s
- Base branch: %s
- Provider: %s
- Dev agents: %d
`, s.RepoURL, s.BaseBranch, s.Provider, s.DevAgents)
	if err := os.WriteFile(ConfigFilePath(), []byte(content), 0644); err != nil {
		return err
	}
	RepoURL = s.RepoURL
	BaseBranch = s.BaseBranch
	Provider = s.Provider
	DevAgentCount = s.DevAgents
	return nil
}

// MustResolveRepoRoot finds the repo root by walking up from the working
// directory (then the executable's directory) looking for the board markers,
// falling back to the absolute working directory.
func MustResolveRepoRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}

	if root, ok := resolveRepoRootFrom(cwd); ok {
		return root
	}

	if executable, err := os.Executable(); err == nil {
		if root, ok := resolveRepoRootFrom(filepath.Dir(executable)); ok {
			return root
		}
	}

	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}

func resolveRepoRootFrom(start string) (string, bool) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}

	for {
		if hasRepoRootMarkers(current) {
			return current, true
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func hasRepoRootMarkers(dir string) bool {
	for _, marker := range repoRootMarkers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			return false
		}
	}
	return true
}

// MustMkdir creates path (and parents), panicking on failure.
func MustMkdir(path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		panic(fmt.Sprintf("failed to create directory %s: %v", path, err))
	}
}

// DefaultLogFilePath is the shared orchestrator log path.
func DefaultLogFilePath() string {
	return filepath.Join(LogsRoot, "orchestrator.log")
}

// NewRunLogFilePath returns a timestamped log path for a single run.
func NewRunLogFilePath(now time.Time) string {
	return filepath.Join(LogsRoot, fmt.Sprintf(
		"orchestrator-%s.log",
		now.Format("20060102-150405.000000000"),
	))
}
