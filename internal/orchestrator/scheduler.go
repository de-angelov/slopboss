package orchestrator

import (
	"sort"
	"strings"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
	"github.com/de-angelov/slopboss/internal/logx"
)

// teamLeadDecomposeSection tags the synthetic task the orchestrator hands the
// team lead when there is no dependency-ready work but blocked tasks remain: a
// decomposition pass whose job is to turn blocked work into runnable (AFK) work.
const teamLeadDecomposeSection = "Team Lead Decomposition"

// decomposeBody drives a decomposition grooming pass. Rather than idling when
// every pending task is blocked, the loop launches this (throttled) so the team
// lead splits the human-required slice out of each blocked task and converts the
// autonomous remainder into assignable AFK work.
const decomposeBody = `No dependency-ready work is available to assign. Do NOT idle. This is a DECOMPOSITION pass: turn blocked work into autonomously-runnable (AFK) work wherever possible, and isolate only what genuinely needs a human.

For each task that cannot currently be assigned:
1. Separate what actually needs a human (a decision, credential, access grant, or an architectural choice) from what a dev agent could do unattended.
2. If part needs a human and part is autonomous, SPLIT it:
   - Create a minimal HITL task capturing ONLY the human-required part, with a one-line, specific ask.
   - Make the remaining autonomous work an AFK task (standard task format, Category: AFK). Depend it on the HITL task ONLY if it truly cannot start until the human acts; otherwise leave it dependency-ready so it can run now.
3. If a task is irreducibly human (no autonomous remainder), keep it HITL with a specific ask.
4. If a human has already resolved a blocker (the required input now exists), flip the unblocked task to Category: AFK so it becomes assignable.

Maintain a single "## Awaiting Human Input" section at the TOP of BACKLOG.md listing each isolated HITL ask as "<TASK-ID> — <exactly what you need from the human>"; remove entries once resolved.

Keep edits minimal and correct, do NOT touch the Dev Agent lanes in TASKS.md, then stop.`

type desiredSession struct {
	Task    board.Task
	TaskKey string
}

func reconcile(tasks []board.Task) {
	done := board.CompletedSet()
	desired := map[string]desiredSession{}
	invalidRoles := map[string]bool{}
	var backlogTask *board.Task

	addDesired := func(role string, task board.Task, taskKey string) {
		if invalidRoles[role] {
			return
		}
		if existing, exists := desired[role]; exists {
			logx.Event("TASKS board error: multiple In Progress tasks for %s (%q and %q); refusing to start role", role, existing.Task.Title, task.Title)
			delete(desired, role)
			invalidRoles[role] = true
			return
		}
		desired[role] = desiredSession{Task: task, TaskKey: taskKey}
	}

	for _, task := range tasks {
		if role, ok := config.DevAgentRoleForActiveSection(task.Section); ok {
			if task.Owner == role && task.Status == "In Progress" {
				addDesired(role, task, task.Key)
			}
			continue
		}
	}

	// Point the team lead at the highest-priority pending backlog task. AFK work
	// is preferred over HITL so idle dev lanes get fed before the pipeline stalls
	// on a human (see board.FirstBacklogTask). Only dependency-ready tasks are
	// returned.
	if bt := board.FirstBacklogTask(tasks); bt.Title != "" {
		backlogTask = &bt
	}

	if backlogTask != nil && len(invalidRoles) == 0 && allDevAgentsBusyDesired(desired) {
		backlogTask = nil
	}

	if backlogTask != nil && len(invalidRoles) > 0 {
		backlogTask = nil
	}

	if backlogTask != nil {
		desired[teamLeadRole] = desiredSession{
			Task:    *backlogTask,
			TaskKey: teamLeadScheduleKey(*backlogTask, tasks, done),
		}
	} else if _, taken := desired[teamLeadRole]; !taken && len(invalidRoles) == 0 {
		// No dependency-ready work. Rather than idle, launch a throttled
		// decomposition grooming pass if pending work is blocked — the team lead
		// splits the human-required slice out and converts the rest to AFK, which
		// board.FirstBacklogTask can then assign. Keyed on the blocked set so it
		// runs once per distinct set of blockers instead of every poll.
		if blocked := board.BlockedBacklogIDs(tasks, done); len(blocked) > 0 {
			task := decompositionTask(blocked)
			desired[teamLeadRole] = desiredSession{Task: task, TaskKey: task.Key}
		}
	}

	now := time.Now()

	mu.Lock()
	for role := range finished {
		if _, stillDesired := desired[role]; !stillDesired {
			delete(failedSessionRetryCounts, failedSessionRetryKey(role, finished[role].TaskKey))
			delete(finished, role)
		}
	}

	for role, session := range running {
		desiredSession, stillDesired := desired[role]
		matches := stillDesired && desiredSession.TaskKey == session.TaskKey
		// A session whose task has already landed (archived Done, or its squash
		// commit is on main) is completing normally — never cancel it; let it exit
		// and remove itself from `running`.
		completing := session.TaskID != "" && done[session.TaskID]
		since, pending := cancelPendingSince[role]

		switch decideCancel(matches, completing, pending, since, now) {
		case cancelKeep:
			delete(cancelPendingSince, role)
		case cancelWait:
			if !pending {
				cancelPendingSince[role] = now
			}
		case cancelNow:
			logx.Event("stopping %s because board files changed", role)
			session.Cancel()
			delete(running, role)
			delete(cancelPendingSince, role)
		}
	}
	mu.Unlock()

	for role, desiredSession := range desired {
		task := desiredSession.Task
		taskKey := desiredSession.TaskKey

		if !git.WorkspaceExists(role) {
			logx.Event("skipping %s because workspace is missing", role)
			continue
		}

		now := time.Now()

		mu.Lock()
		_, exists := running[role]
		finishedSession, alreadyFinished := finished[role]
		if alreadyFinished && finishedSession.TaskKey != taskKey {
			delete(failedSessionRetryCounts, failedSessionRetryKey(role, finishedSession.TaskKey))
			delete(finished, role)
			alreadyFinished = false
		}
		canRetry := false
		if alreadyFinished {
			canRetry, _ = canRetryFailedSessionLocked(role, finishedSession, now)
		}
		if canRetry {
			delete(finished, role)
			alreadyFinished = false
		}
		resumeAfterUsageLimit := alreadyFinished && shouldResumeAfterUsageLimitLocked(finishedSession, now)
		if resumeAfterUsageLimit {
			delete(finished, role)
			alreadyFinished = false
		}
		mu.Unlock()

		if resumeAfterUsageLimit {
			logx.Event("resuming %s after usage-limit cooldown elapsed", role)
		}

		if exists || alreadyFinished {
			continue
		}

		if paused, until := codexLaunchPaused(time.Now()); paused {
			logx.Event("skipping %s because Codex usage limit pause is active until %s", role, until.Format(time.RFC3339))
			continue
		}

		// Charge a retry only now that the launch is actually happening.
		if canRetry {
			mu.Lock()
			retryCount := recordFailedSessionRetryLocked(role, taskKey)
			mu.Unlock()
			logx.Event(
				"retrying %s after failed session for same task (%d/%d)",
				role,
				retryCount,
				config.MaxFailedSessionRetries,
			)
		}

		startSession(role, task, taskKey, tasks)
	}
}

func teamLeadScheduleKey(task board.Task, tasks []board.Task, done map[string]bool) string {
	activeLaneKeys := make([]string, 0, config.DevAgentCount)
	for _, task := range tasks {
		if role, ok := config.DevAgentRoleForActiveSection(task.Section); ok &&
			task.Owner == role &&
			task.Status == "In Progress" {
			activeLaneKeys = append(activeLaneKeys, role+"="+task.Key)
		}
	}
	sort.Strings(activeLaneKeys)

	doneIDs := make([]string, 0, len(done))
	for id := range done {
		doneIDs = append(doneIDs, id)
	}
	sort.Strings(doneIDs)

	return strings.Join([]string{
		task.Key,
		"lanes=" + strings.Join(activeLaneKeys, ","),
		"done=" + strings.Join(doneIDs, ","),
	}, "\x00")
}

func allDevAgentsBusyDesired(desired map[string]desiredSession) bool {
	for k := 1; k <= config.DevAgentCount; k++ {
		if _, busy := desired[config.DevAgentRole(k)]; !busy {
			return false
		}
	}
	return true
}

// cancelAction is the decision reconcile makes about a running session whose
// board match was just evaluated.
type cancelAction int

const (
	cancelKeep cancelAction = iota // board still wants it, or it is completing — clear any pending cancel
	cancelWait                     // mismatch has not yet outlasted the grace period — keep waiting
	cancelNow                      // mismatch persisted past the grace period — cancel the session
)

// decideCancel encodes the cancellation policy as a pure function so the
// completion/debounce rules are unit-testable without spawning sessions. A
// session is kept whenever the board still wants exactly it (matches) or its task
// has already landed (completing). Otherwise the mismatch must persist for
// CancelGracePeriod — measured from `since`, the moment it first went pending —
// before the session is actually cancelled, so a completion-time board rewrite or
// a transient team-lead reshuffle does not kill live work.
func decideCancel(matches, completing, pending bool, since, now time.Time) cancelAction {
	if matches || completing {
		return cancelKeep
	}
	if !pending {
		return cancelWait
	}
	if now.Sub(since) < config.CancelGracePeriod {
		return cancelWait
	}
	return cancelNow
}

// decompositionTask builds the synthetic task that drives a decomposition
// grooming pass. Its Key encodes the blocked set so the finished-session guard
// stops it re-running until the blockers actually change.
func decompositionTask(blockedIDs []string) board.Task {
	return board.Task{
		Section: teamLeadDecomposeSection,
		Title:   "Decompose blocked work into assignable tasks",
		Status:  "In Progress",
		Body:    decomposeBody,
		Key:     "decompose\x00" + strings.Join(blockedIDs, ","),
	}
}

// canRetryFailedSessionLocked reports whether a failed session for the same task
// is eligible to retry, along with the number of retries already consumed. It has
// no side effects: the retry is only counted when a launch actually happens (see
// recordFailedSessionRetryLocked), so a retry deferred by an active pause or a
// stale board entry is never charged. Callers must hold mu.
func canRetryFailedSessionLocked(role string, session FinishedSession, now time.Time) (bool, int) {
	if session.Outcome != sessionFailed {
		return false, 0
	}
	if now.Sub(session.FinishedAt) < config.FailedSessionRetryDelay {
		return false, 0
	}

	retries := failedSessionRetryCounts[failedSessionRetryKey(role, session.TaskKey)]
	if retries >= config.MaxFailedSessionRetries {
		return false, retries
	}
	return true, retries
}

// recordFailedSessionRetryLocked consumes one retry for the task and returns the
// new count. Callers must hold mu.
func recordFailedSessionRetryLocked(role string, taskKey string) int {
	key := failedSessionRetryKey(role, taskKey)
	failedSessionRetryCounts[key]++
	return failedSessionRetryCounts[key]
}

func failedSessionRetryKey(role string, taskKey string) string {
	return role + "\x00" + taskKey
}

func codexLaunchPaused(now time.Time) (bool, time.Time) {
	mu.Lock()
	defer mu.Unlock()

	if !codexPauseActiveLocked(now) {
		return false, time.Time{}
	}
	return true, codexPausedUntil
}

// codexPauseActiveLocked reports whether the usage-limit cooldown is still in
// effect. Callers must hold mu.
func codexPauseActiveLocked(now time.Time) bool {
	return !codexPausedUntil.IsZero() && now.Before(codexPausedUntil)
}

// shouldResumeAfterUsageLimitLocked reports whether a usage-limited session's
// finished record should be cleared so the lane can resume. A usage limit is an
// external cooldown, not a task failure, so once the cooldown elapses the lane
// resumes without consuming a failure retry. Callers must hold mu.
func shouldResumeAfterUsageLimitLocked(session FinishedSession, now time.Time) bool {
	return session.Outcome == sessionUsageLimited && !codexPauseActiveLocked(now)
}

func pauseCodexLaunchesAfterUsageLimit(now time.Time) time.Time {
	until := now.Add(config.CodexUsageLimitCooldown)

	mu.Lock()
	if until.After(codexPausedUntil) {
		codexPausedUntil = until
	}
	activeUntil := codexPausedUntil
	mu.Unlock()

	logx.Event("pausing Codex launches after usage-limit error until %s", activeUntil.Format(time.RFC3339))
	return activeUntil
}
