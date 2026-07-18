package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
	"github.com/de-angelov/slopboss/internal/logx"
	"github.com/de-angelov/slopboss/internal/prompt"
	"github.com/de-angelov/slopboss/internal/provider"
)

// shutdownSessions cancels every running session and waits for their goroutines
// to drain (each removes itself from `running` on exit), up to the grace period.
// Cancelling a session kills the backend's whole process group, so child shells
// and test runners are not left orphaned when the orchestrator exits.
func shutdownSessions() {
	mu.Lock()
	pending := make([]RunningSession, 0, len(running))
	for _, session := range running {
		pending = append(pending, session)
	}
	mu.Unlock()

	if len(pending) == 0 {
		return
	}

	for _, session := range pending {
		logx.Event("cancelling %s session for shutdown", session.Role)
		session.Cancel()
	}

	deadline := time.Now().Add(config.ShutdownGracePeriod)
	for {
		mu.Lock()
		remaining := len(running)
		mu.Unlock()
		if remaining == 0 {
			logx.Event("all sessions stopped")
			return
		}
		if !time.Now().Before(deadline) {
			logx.Event("shutdown grace period elapsed with %d session(s) still running", remaining)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func startSession(role string, task board.Task, taskKey string, tasks []board.Task) {
	ctx, cancel := context.WithCancel(context.Background())

	mu.Lock()
	running[role] = RunningSession{
		Role:    role,
		Task:    task.Title,
		TaskID:  task.ID,
		TaskKey: taskKey,
		Branch:  task.Branch,
		Cancel:  cancel,
	}
	mu.Unlock()

	logx.Event("starting %s on %s", role, task.Title)

	go func() {
		outcome := sessionUnknown
		defer func() {
			mu.Lock()
			delete(running, role)
			delete(cancelPendingSince, role)
			if outcome != sessionCancelled {
				finished[role] = FinishedSession{
					Role:       role,
					Task:       task.Title,
					TaskKey:    taskKey,
					Branch:     task.Branch,
					Outcome:    outcome,
					FinishedAt: time.Now(),
				}
			}
			if outcome == sessionCompleted {
				delete(failedSessionRetryCounts, failedSessionRetryKey(role, taskKey))
			}
			mu.Unlock()
		}()

		switch {
		case role == teamLeadRole:
			outcome = runTeamLead(ctx, task, tasks)
		default:
			if idx, ok := config.DevAgentIndexForRole(role); ok {
				outcome = runAgent(ctx, role, config.DevAgentWorkspace(idx), task, tasks)
			}
		}
	}()
}

func runAgent(ctx context.Context, role string, workspace string, task board.Task, tasks []board.Task) SessionOutcome {
	logx.Event("starting %s on %s in %s", role, task.Title, workspace)

	if task.Branch != "" {
		if err := git.PrepareBranch(workspace, task.Branch); err != nil {
			logx.Event("failed to prepare branch %s in %s: %v", task.Branch, workspace, err)
			return sessionFailed
		}
	}

	p := prompt.BuildPrompt(role, task, tasks, prompt.DevAgentRuntimeInstructions())

	return runSession(ctx, role, workspace, p, sessionProvider)
}

func runTeamLead(ctx context.Context, task board.Task, tasks []board.Task) SessionOutcome {
	git.RunGit(config.TeamLeadPath, "fetch", "--all", "--prune")

	currentBranch := git.CurrentBranchName(config.TeamLeadPath)
	if currentBranch != "" {
		logx.Event("team lead branch: %s", currentBranch)
	}

	p := prompt.BuildPrompt(teamLeadRole, task, tasks, fmt.Sprintf(`
Role: Team Lead Agent
Runtime Rules:
- No code implementation during grooming. Do not review dev-agent branches or merge them. Maintain sensible backlog priorities.
- If dev-agent lanes are Blocked by unrelated verification failures, create or prioritize a narrow unblocker task before assigning lower-priority feature work.
- Once an unblocker is archived as Done, update dependent blocked tasks back to Status: In Progress with a progress note.

Efficiency Rules (keep this session small — you are hard-capped at %d turns):
- The BOARD CONTEXT below is your primary source of truth. It lists every backlog and lane task with its ID, Status, Category, Owner, Dependencies, and Blocking tasks, plus the full list of completed task IDs. A task is unblocked when all of its Dependencies appear in that completed-ID list. Resolve dependencies from that list — do NOT read ARCHIVE.md to check completion status.
- You MAY read the full body of the specific task(s) you are about to assign or re-prioritize (for scope and non-overlap), and you MAY 'grep "Task ID: X"' a single file for one fact. Do these sparingly.
- Do NOT slurp whole files (no full ARCHIVE.md read) and do NOT re-derive board state from git: no repo-wide 'grep -rn', no 'git log -N', no 'ls -R', no 'git fetch' or branch enumeration. Every command's output is re-sent on every later turn, so keep each output tiny and targeted.
- Decide, then write. Apply your assignment/priority decisions in as few board edits as possible — batch edits to a file rather than many small rewrites.
- If you cannot finish within the turn budget, apply the highest-priority board change first; the next grooming pass resumes from the updated board.
`, config.TeamLeadMaxTurns))

	return runSession(ctx, teamLeadRole, config.TeamLeadPath, p, sessionProvider)
}

func runSession(ctx context.Context, role string, workspace string, promptText string, p provider.Provider) SessionOutcome {
	model := p.DefaultModel(role)

	// Cap the team lead's agentic turns; dev agents run uncapped because they do
	// real implementation that legitimately needs many turns.
	maxTurns := 0
	if role == teamLeadRole {
		maxTurns = config.TeamLeadMaxTurns
	}

	cmd := p.Command(ctx, model, maxTurns)
	cmd.Dir = workspace
	cmd.Stdin = strings.NewReader(promptText)
	if role != teamLeadRole {
		cmd.Env = packageManagerTimeoutEnv(os.Environ())
	}

	// The monitor only parses; tee the raw stream to the orchestrator log too.
	monitor := p.NewMonitor()
	sink := io.MultiWriter(monitor, logx.Writer{})
	cmd.Stdout = sink
	cmd.Stderr = sink

	modelLabel := model
	if modelLabel == "" {
		modelLabel = "configured default"
	}
	logx.Event("running %s session in %s with model %s", p.Name(), workspace, modelLabel)

	err := cmd.Run()
	usage := monitor.Breakdown()
	logCodexTokenBreakdown(role, usage)

	if ctx.Err() == context.Canceled {
		logx.Event("%s session cancelled in %s", p.Name(), workspace)
		return sessionCancelled
	}

	if err != nil {
		if monitor.UsageLimited() {
			pauseCodexLaunchesAfterUsageLimit(time.Now())
			logx.Event("%s usage limit reached in %s: %v", p.Name(), workspace, err)
			return sessionUsageLimited
		}
		logx.Event("%s failed in %s: %v", p.Name(), workspace, err)
		return sessionFailed
	}

	logx.Event("%s completed in %s", p.Name(), workspace)
	return sessionCompleted
}

func packageManagerTimeoutEnv(base []string) []string {
	return append(base,
		"npm_config_fetch_retries=2",
		"npm_config_fetch_timeout=120000",
		"npm_config_fetch_retry_mintimeout=10000",
		"npm_config_fetch_retry_maxtimeout=30000",
		"NPM_CONFIG_FETCH_RETRIES=2",
		"NPM_CONFIG_FETCH_TIMEOUT=120000",
		"NPM_CONFIG_FETCH_RETRY_MINTIMEOUT=10000",
		"NPM_CONFIG_FETCH_RETRY_MAXTIMEOUT=30000",
	)
}

// logCodexTokenBreakdown surfaces where a session's tokens actually went. A large
// total is often dominated by cached_input (cheap re-sends of the growing
// transcript); the fresh input, output, and reasoning tokens are what drive cost.
// Splitting them out makes it clear whether a ~1M-token session is expensive or
// mostly cache reads.
func logCodexTokenBreakdown(role string, usage provider.TokenBreakdown) {
	if usage.Total == 0 && usage.Input == 0 && usage.Output == 0 {
		return
	}

	freshInput := usage.Input - usage.CachedInput
	if freshInput < 0 {
		freshInput = 0
	}

	logx.Event(
		"%s session token breakdown — total: %d | input: %d (cached: %d, fresh: %d) | output: %d (reasoning: %d)",
		role,
		usage.Total,
		usage.Input,
		usage.CachedInput,
		freshInput,
		usage.Output,
		usage.ReasoningOutput,
	)
}
