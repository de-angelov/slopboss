package orchestrator

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/coordination"
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

func startSession(role string, task board.Task, taskKey string, tasks []board.Task, leaseID string) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, processLeaseKey{}, leaseID)

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
		var evidence string
		defer func() {
			if outcome != sessionUnknown {
				if err := reportSessionTerminal(leaseID, outcome, role, taskKey, evidence); err != nil {
					logx.Event("cannot report %s terminal result: %v", role, err)
				}
			}
			mu.Lock()
			delete(running, role)
			delete(cancelPendingSince, role)
			if outcome != sessionCancelled && outcome != sessionUsageLimited {
				recordCircuitResultLocked(circuitKey(role, taskKey), outcome == sessionCompleted, time.Now())
			}
			mu.Unlock()
		}()

		switch {
		case role == teamLeadRole:
			outcome, evidence = runTeamLead(ctx, task, tasks)
		default:
			if idx, ok := config.DevAgentIndexForRole(role); ok {
				workspace := config.DevAgentWorkspace(idx)
				outcome = runAgent(ctx, role, workspace, task, tasks)
				if outcome == sessionCompleted {
					landOutcome, landEvidence := LandCompletedTask(ctx, task, role, workspace)
					outcome = landOutcome
					evidence = landEvidence
				}
			}
		}
	}()
}

func runAgent(ctx context.Context, role string, workspace string, task board.Task, tasks []board.Task) SessionOutcome {
	model := config.ProviderModel(sessionProvider.Name(), role, task.Category)
	logx.Event("starting %s on %s in %s (model: %s)", role, task.Title, workspace, model)

	if task.Branch != "" {
		if err := git.PrepareBranch(workspace, task.Branch); err != nil {
			logx.Event("failed to prepare branch %s in %s: %v", task.Branch, workspace, err)
			return sessionFailed
		}
	}

	p := prompt.BuildPrompt(role, task, tasks, prompt.DevAgentRuntimeInstructions())

	return runSessionWithReportAndModel(ctx, role, workspace, p, sessionProvider, "", model)
}

func runTeamLead(ctx context.Context, task board.Task, tasks []board.Task) (SessionOutcome, string) {
	git.RunGit(config.TeamLeadPath, "fetch", "--all", "--prune")

	currentBranch := git.CurrentBranchName(config.TeamLeadPath)
	if currentBranch != "" {
		logx.Event("team lead branch: %s", currentBranch)
	}

	model := config.ProviderModel(sessionProvider.Name(), teamLeadRole, task.Category)
	logx.Event("starting %s on %s (model: %s)", teamLeadRole, task.Title, model)

	maxTurns := config.TeamLeadMaxTurns
	if task.Section == teamLeadIdleAuditSection {
		maxTurns = config.IdleAuditMaxTurns
	}

	rules := fmt.Sprintf(`
Role: Team Lead Agent
Runtime Rules:
- No code implementation during grooming. Maintain sensible backlog priorities. For a Completion Follow-Up task only, inspect the named assigned branches and, after focused verification, rebase, squash-merge, push, and archive them; otherwise do not review or merge dev-agent branches.
- The board may change during this session. Re-read the relevant task and lane before making an assignment, moving work, or directing a worker; do not rely on the initial snapshot for those decisions.
- A dev-agent lane is occupied only by its In Progress task. Blocked or rejected tasks do not consume lane capacity; assign ready work into any lane with no In Progress task.
- When a task becomes Blocked, immediately move it out of its named "Dev Agent N In Progress" section into the shared "Blocked Tasks" section, set Owner to Unassigned, preserve its branch and blocker evidence, then fill the freed lane with the highest-priority ready non-overlapping task. Do not leave a blocked task assigned to a dev agent.
- Verification failures outside a dev task are not blockers. The dev agent notes them and continues; do not create a blocked lane for them.
- Every concrete technical blocker must have an owning board task. If no existing task explicitly owns the fix, create a focused AFK task with slopboss board create, link its ID in the blocked task, and preserve exact failure evidence. Do not finish reconciliation with orphan blockers.
- Once every dependency of a blocked task is archived as Done, move it from "Blocked Tasks" into a free "Dev Agent N In Progress" lane, set Owner to that agent and Status to In Progress, then add a progress note. Do not merely change its status while it remains unassigned.

Efficiency Rules (keep this session small — you are hard-capped at %d turns):
- The BOARD CONTEXT below is your primary source of truth. It lists every backlog and lane task with its ID, Status, Category, Owner, Dependencies, and Blocking tasks, plus the full list of completed task IDs. A task is unblocked when all of its Dependencies appear in that completed-ID list. Resolve dependencies from that list — do NOT read ARCHIVE.md to check completion status.
- You MAY read the full body of the specific task(s) you are about to assign or re-prioritize (for scope and non-overlap), and you MAY 'grep "Task ID: X"' a single file for one fact. Do these sparingly.
- Do NOT slurp whole files (no full ARCHIVE.md read) and do NOT re-derive board state from git: no repo-wide 'grep -rn', no 'git log -N', no 'ls -R', no 'git fetch' or branch enumeration. Every command's output is re-sent on every later turn, so keep each output tiny and targeted.
- Decide, then write. Apply your assignment/priority decisions in as few board edits as possible — batch edits to a file rather than many small rewrites.
- If you cannot finish within the turn budget, apply the highest-priority board change first; the next grooming pass resumes from the updated board.
`, maxTurns)
	if isAgentLeadTask(task) {
		rules = fmt.Sprintf(`Role: Team Lead Agent
Decide the next useful coordination actions from the supplied board and worker snapshot. Use slopboss board commands to make structural changes; do not edit shared board files directly. You may inspect and review relevant worker branches, and merge only after focused verification. Do not implement product code. Preserve uncertainty and report concrete evidence. If there is nothing useful to do, finish without changes. You are limited to %d turns.`, maxTurns)
	}
	executable, err := os.Executable()
	if err == nil {
		rules += "\nUse this exact command prefix for board operations: " + shellQuote(executable) + " --dir " + shellQuote(config.RepoRoot) + ". Never use a different slopboss from PATH. To clear a completed task from Awaiting Human Input, use this prefix with board clear-human TASK-ID. To resume a finished dev agent to rebase or continue work, use this prefix with board resume TASK-ID --reason \"specific instructions\". Never write markdown messages to dev agents in task bodies or TASKS.md; finished workers do not read them and will not restart without board resume.\n"
	}
	var reportPath string
	if isAgentLeadTask(task) {
		if err := os.MkdirAll(config.WorkspacesRoot, 0755); err != nil {
			return sessionBlocked, "cannot create coordination report directory: " + err.Error()
		}
		f, err := os.CreateTemp(config.WorkspacesRoot, ".coordination-report-*")
		if err != nil {
			return sessionBlocked, "cannot create coordination report: " + err.Error()
		}
		reportPath = f.Name()
		if err := f.Close(); err != nil {
			return sessionBlocked, "cannot close coordination report: " + err.Error()
		}
		defer os.Remove(reportPath)
		rules += "\nBefore finishing, call the exact executable with coordination report --status STATUS --summary SUMMARY, choosing exactly one STATUS: acted, idle, or blocked. Use blocked if an intended action failed or remains uncertain; use idle only when no action is needed. A missing report requires human review.\n"
	}
	p := prompt.BuildPrompt(teamLeadRole, task, tasks, rules)

	before := boardStateHash()
	outcome := runSessionWithReportAndModel(ctx, teamLeadRole, config.TeamLeadPath, p, sessionProvider, reportPath, model)
	if isAgentLeadTask(task) && outcome == sessionCompleted {
		return classifyCoordinationReport(reportPath)
	}
	if outcome == sessionCompleted && task.Section == teamLeadIdleAuditSection {
		mu.Lock()
		lastAuditedBoardHash = boardStateHash()
		mu.Unlock()
	}
	if outcome == sessionCompleted && task.Section != teamLeadIdleAuditSection && !isAgentLeadTask(task) && before != "" && boardStateHash() == before {
		logx.Event("team lead completed without changing the board; retrying after cooldown")
		return sessionFailed, ""
	}
	return outcome, ""
}

func classifyCoordinationReport(path string) (SessionOutcome, string) {
	report, err := coordination.ReadReport(path)
	if err != nil {
		return sessionBlocked, "coordination result missing or invalid: " + err.Error()
	}
	evidence := "coordination " + report.Status + ": " + strings.TrimSpace(report.Summary)
	if report.Status == "blocked" {
		return sessionBlocked, evidence
	}
	return sessionCompleted, evidence
}

// boardStateHash fingerprints the three board files so a Team Lead session that
// exits successfully without changing anything is not mistaken for progress.
func boardStateHash() string {
	h := sha256.New()
	for _, path := range []string{config.BacklogFile, config.TasksFile, config.ArchiveFile} {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		h.Write(data)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func runSession(ctx context.Context, role string, workspace string, promptText string, p provider.Provider) SessionOutcome {
	return runSessionWithReportAndModel(ctx, role, workspace, promptText, p, "", "")
}

func runSessionWithReport(ctx context.Context, role string, workspace string, promptText string, p provider.Provider, reportPath string) SessionOutcome {
	return runSessionWithReportAndModel(ctx, role, workspace, promptText, p, reportPath, "")
}

func runSessionWithReportAndModel(ctx context.Context, role string, workspace string, promptText string, p provider.Provider, reportPath, modelOverride string) SessionOutcome {
	model := modelOverride
	if model == "" {
		model = p.DefaultModel(role)
	}

	// Cap agentic turns to prevent runaway loops from burning tokens.
	maxTurns := config.DevAgentMaxTurns
	if role == teamLeadRole {
		maxTurns = config.TeamLeadMaxTurns
	}

	cmd := p.Command(ctx, model, maxTurns)
	cmd.Dir = workspace
	cmd.Stdin = strings.NewReader(promptText)
	if role != teamLeadRole {
		cmd.Env = packageManagerTimeoutEnv(os.Environ())
	}
	if reportPath != "" {
		cmd.Env = append(os.Environ(), coordination.ReportPathEnv+"="+reportPath)
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

	err := cmd.Start()
	if err == nil {
		if leaseID, ok := ctx.Value(processLeaseKey{}).(string); ok && leaseID != "" {
			if recordErr := recordWorkerProcess(leaseID, cmd.Process.Pid); recordErr != nil {
				logx.Event("cannot record %s process identity: %v", role, recordErr)
			}
		}
		err = cmd.Wait()
	}
	usage := provider.FinalUsage(ctx, p, monitor)
	logTokenBreakdown(role, usage)

	if ctx.Err() == context.Canceled {
		logx.Event("%s session cancelled in %s", p.Name(), workspace)
		return sessionCancelled
	}

	if err != nil {
		isUsage := monitor.UsageLimited()
		if !isUsage {
			if reporter, ok := monitor.(provider.ErrorReporter); ok && isQuotaOrRateLimit(reporter.ErrorMessage()) {
				isUsage = true
			} else if isQuotaOrRateLimit(err.Error()) {
				isUsage = true
			}
		}
		if isUsage {
			pauseLaunchesAfterUsageLimit(time.Now())
			logx.Event("%s usage limit reached in %s: %v", p.Name(), workspace, err)
			return sessionUsageLimited
		}
		if reporter, ok := monitor.(provider.ErrorReporter); ok {
			if msg := reporter.ErrorMessage(); msg != "" {
				logx.Event("%s failed in %s: %v (%s)", p.Name(), workspace, err, msg)
				return sessionFailed
			}
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

// logTokenBreakdown surfaces where a session's tokens actually went. A large
// total is often dominated by cached_input (cheap re-sends of the growing
// transcript); the fresh input, output, and reasoning tokens are what drive cost.
// Splitting them out makes it clear whether a ~1M-token session is expensive or
// mostly cache reads.
func logTokenBreakdown(role string, usage provider.TokenBreakdown) {
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

func isQuotaOrRateLimit(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "resource_exhausted")
}
