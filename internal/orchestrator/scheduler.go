package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/logx"
)

func loadControllerState() (ControllerState, error) {
	state, err := controllerStateStore.Load()
	if errors.Is(err, os.ErrNotExist) {
		state = ControllerState{Version: ControllerStateVersion}
		return state, nil
	}
	return state, err
}

func commitControllerState(state ControllerState) error { return controllerStateStore.Commit(state) }

// reportSessionTerminal accepts worker execution evidence as a controller
// event. Only this controller-side handler changes the durable lease snapshot.
func reportSessionTerminal(leaseID string, outcome SessionOutcome, role, taskKey string, details ...string) error {
	controllerMu.Lock()
	defer controllerMu.Unlock()
	state, err := loadControllerState()
	if err != nil {
		return err
	}
	for i := range state.Leases {
		if state.Leases[i].LeaseID != leaseID {
			continue
		}
		evidence := terminalEvidence(outcome, role, taskKey)
		if len(details) > 0 && details[0] != "" {
			evidence += "; " + details[0]
		}
		if prior := state.Leases[i].Terminal; prior != nil {
			if prior.Outcome == string(outcome) && prior.Evidence == evidence {
				return nil
			}
			return fmt.Errorf("controller lease %q already has terminal result %q", leaseID, prior.Outcome)
		}
		finishedAt := time.Now()
		state.Leases[i].HeartbeatAt = finishedAt
		state.Leases[i].Terminal = &ControllerTerminalResult{Outcome: string(outcome), FinishedAt: finishedAt, Evidence: evidence}
		if state.History == nil {
			state.History = make(map[string]ControllerWorkerLease)
		}
		state.History[leaseID] = state.Leases[i]
		return commitControllerState(state)
	}
	return fmt.Errorf("controller lease %q not found for terminal event", leaseID)
}

func commitRecoveryLease(state ControllerState, lease ControllerWorkerLease, taskKey string) error {
	if previous, found := findControllerLease(state, lease.Intent.Role); found && previous.Terminal != nil {
		if state.History == nil {
			state.History = make(map[string]ControllerWorkerLease)
		}
		state.History[previous.LeaseID] = previous
		if previous.LeaseID == lease.LeaseID {
			state.PastResults = append(state.PastResults, previous)
		}
		delete(state.RetryApprovals, previous.LeaseID)
	}
	state.Leases = replaceControllerLease(state.Leases, lease)
	delete(state.RetryApprovals, lease.LeaseID)
	if err := commitControllerState(state); err != nil {
		return err
	}
	mu.Lock()
	delete(recoveryDecisions, taskKey)
	mu.Unlock()
	return nil
}

func recordRecoveryDecision(taskKey, reason string) {
	if reason == "" {
		return
	}
	mu.Lock()
	recoveryDecisions[taskKey] = reason
	mu.Unlock()
}

func controllerTaskID(task board.Task, key string) string {
	if task.ID != "" {
		return task.ID
	}
	return key
}
func controllerBranch(task board.Task, role string) string {
	if task.Branch != "" {
		return task.Branch
	}
	return "synthetic/" + strings.NewReplacer(" ", "-", "\\", "-").Replace(role)
}
func controllerLeaseID(role string, task board.Task, key string) string {
	sum := sha256.Sum256([]byte(role + "\x00" + controllerTaskID(task, key) + "\x00" + key))
	return hex.EncodeToString(sum[:])
}
func findControllerLease(state ControllerState, role string) (ControllerWorkerLease, bool) {
	for _, lease := range state.Leases {
		if lease.Intent.Role == role {
			return lease, true
		}
	}
	return ControllerWorkerLease{}, false
}

func recoveryLeaseDecision(state ControllerState, role, leaseID string) (ControllerWorkerLease, bool, string) {
	lease, found := findControllerLease(state, role)
	if !found {
		return ControllerWorkerLease{}, true, ""
	}
	if lease.Terminal == nil && time.Since(lease.HeartbeatAt) >= 10*time.Minute {
		return lease, false, "Blocked: worker lease has no terminal evidence; verify worker stopped and use slopboss recover"
	}
	if lease.Terminal != nil && lease.Terminal.Outcome == string(sessionBlocked) && state.RetryApprovals[lease.LeaseID] == "" {
		return lease, false, "Blocked: coordination requires review (" + lease.Terminal.Evidence + ")"
	}
	if lease.LeaseID != leaseID && lease.Terminal != nil {
		if previous, ok := state.History[leaseID]; ok {
			lease = previous
		}
	}
	if lease.LeaseID == leaseID && lease.Terminal != nil {
		if state.RetryApprovals[leaseID] != "" {
			return lease, true, ""
		}
		completedPass := retryableCompletedTeamLeadPass(role, lease.Intent.TaskID, lease.Terminal.Outcome)
		if retryableDurableResult(lease) || completedPass {
			if lease.Attempt > config.MaxFailedSessionRetries && lease.Terminal.Outcome != string(sessionCancelled) && lease.Terminal.Outcome != string(sessionUsageLimited) {
				return lease, false, fmt.Sprintf("Blocked: retry attempts exhausted (%d); Team Lead review required (%s)", lease.Attempt-1, lease.Terminal.Evidence)
			}
			return lease, true, ""
		}
		if lease.Terminal.Outcome != string(sessionCompleted) {
			return lease, false, "Blocked: ambiguous worker result; Team Lead review required (" + lease.Terminal.Evidence + ")"
		}
		return lease, false, ""
	}
	if lease.LeaseID == leaseID || lease.Terminal == nil {
		return lease, false, ""
	}
	return lease, true, ""
}

// continuationTaskKey accepts a continuation only when it cites terminal
// evidence for this exact assignment. The terminal lease stays in history.
func continuationTaskKey(task board.Task, role string, state ControllerState) string {
	if task.ContinueAfter == "" {
		return task.Key
	}
	prior, ok := state.History[task.ContinueAfter]
	if !ok {
		prior, ok = findControllerLease(state, role)
	}
	if !ok || prior.LeaseID != task.ContinueAfter || prior.Terminal == nil || prior.Intent.Role != role || prior.Intent.TaskID != task.ID || prior.Intent.Branch != controllerBranch(task, role) {
		return task.Key
	}
	return task.Key + "\x00continue\x00" + prior.LeaseID
}

func retryableCompletedTeamLeadPass(role, taskKey, outcome string) bool {
	return role == teamLeadRole && outcome == string(sessionCompleted) &&
		(strings.HasPrefix(taskKey, "decompose\x00") || strings.HasPrefix(taskKey, "unblock\x00"))
}

// retryableDurableResult accepts only outcomes whose controller evidence is
// recognized as transient/interrupted. Unknown outcomes require human review.
func retryableDurableResult(lease ControllerWorkerLease) bool {
	if lease.Terminal == nil {
		return false
	}
	switch SessionOutcome(lease.Terminal.Outcome) {
	case sessionFailed, sessionCancelled, sessionUsageLimited:
		return true
	default:
		return false
	}
}
func replaceControllerLease(leases []ControllerWorkerLease, lease ControllerWorkerLease) []ControllerWorkerLease {
	for i := range leases {
		if leases[i].Intent.Role == lease.Intent.Role {
			leases[i] = lease
			return leases
		}
	}
	return append(leases, lease)
}
func restoreTerminalLease(lease ControllerWorkerLease, task board.Task, taskKey string) {
	if lease.Terminal == nil {
		return
	}
	outcome := SessionOutcome(lease.Terminal.Outcome)
	mu.Lock()
	if existing, exists := finished[lease.Intent.Role]; !exists || existing.TaskKey != taskKey {
		finished[lease.Intent.Role] = FinishedSession{Role: lease.Intent.Role, Task: task.Title, TaskKey: taskKey, Branch: lease.Intent.Branch, Outcome: outcome, FinishedAt: lease.Terminal.FinishedAt}
	}
	mu.Unlock()
}
func terminalEvidence(outcome SessionOutcome, role, taskKey string) string {
	return fmt.Sprintf("session outcome=%s role=%q task=%q", outcome, role, taskKey)
}

// teamLeadDecomposeSection tags the synthetic task the orchestrator hands the
// team lead when there is no dependency-ready work but blocked tasks remain: a
// decomposition pass whose job is to turn blocked work into runnable (AFK) work.
const teamLeadDecomposeSection = "Team Lead Decomposition"

// teamLeadFollowUpSection tags the synthetic task scheduled after a dev-agent
// session ends. It gives the Team Lead one bounded pass to close the loop before
// more work is assigned.
const teamLeadFollowUpSection = "Team Lead Completion Follow-Up"

const teamLeadIdleAuditSection = "Team Lead Idle Audit"

const teamLeadUnblockSection = "Team Lead Unblock"

const teamLeadIdleAuditInterval = 30 * time.Minute

// decomposeBody drives a decomposition grooming pass. Rather than idling when
// every pending task is blocked, the loop launches this (throttled) so the team
// lead splits the human-required slice out of each blocked task and converts the
// autonomous remainder into assignable AFK work.
const decomposeBody = `No dependency-ready work is available to assign. Do NOT idle. This is a DECOMPOSITION pass: turn blocked work into autonomously-runnable (AFK) work wherever possible, and isolate only what genuinely needs a human.

Every concrete technical blocker must have an owning task. If no existing task explicitly owns the fix, create a focused AFK task using slopboss board create TASK-ID --title ... --category AFK --dependencies ... --body ... . Include Objective, Scope, Out of Scope, Acceptance Criteria, and exact Verification. Link its ID in the blocked task's dependencies/blocking tasks and preserve exact failure evidence. Never finish with an orphan blocker.

For each task that cannot currently be assigned:
1. Separate what actually needs a human (a decision, credential, access grant, or an architectural choice) from what a dev agent could do unattended.
2. If part needs a human and part is autonomous, SPLIT it:
   - Create a minimal HITL task capturing ONLY the human-required part, with a one-line, specific ask.
   - Make the remaining autonomous work an AFK task (standard task format, Category: AFK). Depend it on the HITL task ONLY if it truly cannot start until the human acts; otherwise leave it dependency-ready so it can run now.
3. If a task is irreducibly human (no autonomous remainder), keep it HITL with a specific ask.
4. If a human has already resolved a blocker (the required input now exists), flip the unblocked task to Category: AFK so it becomes assignable.

Maintain a single "## Awaiting Human Input" section at the TOP of BACKLOG.md listing each isolated HITL ask as "<TASK-ID> — <exactly what you need from the human>"; remove entries once resolved.

Keep edits minimal and correct, do NOT touch the Dev Agent lanes in TASKS.md. Stop only after every blocked task has an explicit owning task or a specific human ask.

After creating owners, re-verify: re-read BACKLOG.md and TASKS.md and confirm each blocked task either (a) has a concrete owning AFK task whose ID appears in its dependencies/blocking tasks, or (b) a single specific ask in "## Awaiting Human Input". If any blocker is still unowned, create the missing task now rather than finishing. If a pass cannot resolve a blocker after this verification, leave a one-line note in the blocked task's progress notes explaining why, and escalate it in "## Awaiting Human Input" with the exact failing command and the smallest human action needed.`

const idleAuditBody = `This is a periodic, low-cost board-health review. Inspect the current board for stale blocker references, tasks stranded in invalid sections/statuses, unowned technical blockers, and ready tasks that are not scheduled. Correct board-only inconsistencies with the structural board commands and create a focused AFK task for any concrete technical issue without an owner. Do not modify product code, inspect unrelated branches, or invent work. If the board is healthy, make no changes and finish.`

// unblockBody drives an unblock pass: a blocked task whose dependencies are now
// satisfied must be moved back into a free Dev Agent lane instead of staying
// stranded in Blocked Tasks.
const unblockBody = `A previously blocked task is now dependency-ready. Move it out of the "Blocked Tasks" section into a free "Dev Agent N In Progress" lane, set Owner to that agent and Status to In Progress, and add a progress note recording which dependency landed. Do not merely change its status while it remains unassigned. If no lane is free, leave it in Blocked Tasks and re-check next poll.`

type desiredSession struct {
	Task    board.Task
	TaskKey string
}

// planTeamLeadAction selects the next Team Lead assignment in priority order.
func planTeamLeadAction(tasks []board.Task, done map[string]bool, durable ControllerState, desired map[string]desiredSession, invalidRoles map[string]bool) (desiredSession, bool) {
	var backlogTask *board.Task

	// Prefer unblocking a ready blocked task over grooming new work: unblocking
	// unlocks downstream work and recovers stalled lanes. Only fall back to the
	// backlog when nothing is ready to unblock.
	if ub := board.UnblockedBlockedTask(tasks, done); ub.Title != "" {
		ut := board.Task{
			Section: teamLeadUnblockSection,
			Title:   "Unblock: " + ub.Title,
			Status:  "In Progress",
			Body:    unblockBody + "\n\nTarget task: " + ub.ID + " (" + ub.Title + ")",
			Key:     "unblock\x00" + ub.Key,
		}
		backlogTask = &ut
	} else if bt := board.FirstBacklogTask(tasks, done); bt.Title != "" {
		backlogTask = &bt
	}

	// Orphan blockers take priority over grooming: a blocked task with no
	// owning task stalls downstream work, so force a decomposition pass even
	// when ready backlog work exists. Once that pass has ended, let unrelated
	// ready work proceed even if the orphan remains unresolved.
	if board.HasOrphanBlocker(tasks) && backlogTask != nil {
		orphanPass := decompositionTask(board.BlockedBacklogIDs(tasks, done))
		id := controllerLeaseID(teamLeadRole, orphanPass, orphanPass.Key)
		lease, found := durable.History[id]
		if !found {
			lease, found = findControllerLease(durable, teamLeadRole)
		}
		if !found || lease.LeaseID != id || lease.Terminal == nil {
			backlogTask = nil
		}
	}

	followUpTask, needsFollowUp := completionFollowUpTask()

	if needsFollowUp {
		return desiredSession{Task: followUpTask, TaskKey: followUpTask.Key}, true
	}
	if backlogTask != nil && len(invalidRoles) == 0 && !allDevAgentsBusyDesired(desired) {
		return desiredSession{
			Task:    *backlogTask,
			TaskKey: teamLeadScheduleKey(*backlogTask, tasks, done),
		}, true
	}

	var candidate desiredSession
	if len(invalidRoles) == 0 {
		// No dependency-ready work. Rather than idle, launch a throttled
		// decomposition grooming pass if pending work is blocked — the team lead
		// splits the human-required slice out and converts the rest to AFK, which
		// board.FirstBacklogTask can then assign. Keyed on the blocked set so it
		// runs once per distinct set of blockers instead of every poll.
		if blocked := board.BlockedBacklogIDs(tasks, done); len(blocked) > 0 {
			task := decompositionTask(blocked)
			candidate = desiredSession{Task: task, TaskKey: task.Key}
		}
	}

	// If all useful Team Lead work is exhausted, run a small periodic board
	// health pass. Its time-bucketed key prevents the loop from launching it on
	// every poll; session start time provides the 30-minute quiet interval.
	mu.Lock()
	due := lastTeamLeadActivity.IsZero() || time.Since(lastTeamLeadActivity) >= teamLeadIdleAuditInterval
	mu.Unlock()
	exhausted := false
	if candidate.TaskKey != "" {
		id := controllerLeaseID(teamLeadRole, candidate.Task, candidate.TaskKey)
		lease, found := durable.History[id]
		if !found {
			lease, found = findControllerLease(durable, teamLeadRole)
		}
		exhausted = found && lease.LeaseID == id && lease.Terminal != nil && lease.Attempt > config.MaxFailedSessionRetries && lease.Terminal.Outcome != string(sessionCancelled) && lease.Terminal.Outcome != string(sessionUsageLimited)
	}
	if exhausted || candidate.TaskKey == "" {
		lease, found := findControllerLease(durable, teamLeadRole)
		if found && lease.Terminal == nil && strings.HasPrefix(lease.Intent.TaskID, "idle-audit\x00") {
			audit := idleAuditTask(time.Now())
			audit.Key = lease.Intent.TaskID
			return desiredSession{Task: audit, TaskKey: audit.Key}, true
		}
	}
	if candidate.TaskKey != "" && !exhausted {
		return candidate, true
	}
	if exhausted && due {
		logx.Event("%s retries exhausted for %v; escalating to idle audit", candidate.Task.Section, candidate.TaskKey)
	}
	if due {
		audit := idleAuditTask(time.Now())
		return desiredSession{Task: audit, TaskKey: audit.Key}, true
	}
	return desiredSession{}, false
}

func reconcile(tasks []board.Task) { reconcileWithMode(tasks, "planner") }

func reconcileWithMode(tasks []board.Task, mode string) {
	controllerMu.Lock()
	defer controllerMu.Unlock()
	durable, err := loadControllerState()
	if err != nil {
		logx.Event("cannot load controller state; refusing launches: %v", err)
		return
	}
	mu.Lock()
	recoveryByRole = map[string]string{}
	for _, lease := range durable.Leases {
		if _, active := running[lease.Intent.Role]; !active && lease.Terminal == nil {
			recoveryByRole[lease.Intent.Role] = "Unresolved lease: verify worker stopped; use slopboss recover"
		}
	}
	mu.Unlock()
	done := board.CompletedSet()
	desired := map[string]desiredSession{}
	invalidRoles := map[string]bool{}

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
			if task.Owner == role && strings.EqualFold(task.Status, "In Progress") {
				addDesired(role, task, continuationTaskKey(task, role, durable))
			}
			continue
		}
	}

	if mode == "agent" {
		desired[teamLeadRole] = coordinationAction(tasks, done, durable)
	} else if action, ok := planTeamLeadAction(tasks, done, durable, desired, invalidRoles); ok {
		desired[teamLeadRole] = action
	}

	now := time.Now()

	mu.Lock()
	for role := range finished {
		if _, stillDesired := desired[role]; !stillDesired {
			delete(finished, role)
		}
	}

	for role, session := range running {
		desiredSession, stillDesired := desired[role]
		matches := stillDesired && desiredSession.TaskKey == session.TaskKey
		// Let a coordinating Team Lead finish; a changed snapshot is handled next.
		if mode == "agent" && role == teamLeadRole && strings.HasPrefix(session.TaskKey, agentLeadPrefix) {
			matches = true
		}
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
			delete(cancelPendingSince, role)
		}
	}
	mu.Unlock()

	for role, desiredSession := range desired {
		task := desiredSession.Task
		taskKey := desiredSession.TaskKey
		mu.Lock()
		_, active := running[role]
		mu.Unlock()
		if active {
			continue
		}
		leaseID := controllerLeaseID(role, task, taskKey)
		lease, shouldLaunch, recoveryReason := recoveryLeaseDecision(durable, role, leaseID)
		if lease.LeaseID == leaseID && lease.Terminal != nil &&
			(lease.Terminal.Outcome == string(sessionFailed) || retryableCompletedTeamLeadPass(role, taskKey, lease.Terminal.Outcome)) {
			restoreTerminalLease(lease, task, taskKey)
		}
		if recoveryReason != "" {
			recordRecoveryDecision(taskKey, recoveryReason)
			mu.Lock()
			recoveryByRole[role] = recoveryReason
			mu.Unlock()
		}
		if !shouldLaunch {
			if lease.LeaseID == leaseID && lease.Terminal != nil {
				restoreTerminalLease(lease, task, taskKey)
			}
			continue
		}

		if !controllerWorkspaceExists(role) {
			logx.Event("skipping %s because workspace is missing", role)
			continue
		}

		now := time.Now()

		mu.Lock()
		_, exists := running[role]
		finishedSession, alreadyFinished := finished[role]
		if alreadyFinished && finishedSession.TaskKey != taskKey {
			delete(finished, role)
			alreadyFinished = false
		}
		canRetry := false
		if alreadyFinished {
			canRetry = canRetryFailedSessionLocked(role, finishedSession, now)
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
		ck := circuitKey(role, taskKey)
		circuitOpen := circuitOpenLocked(ck, now)
		mu.Unlock()

		if resumeAfterUsageLimit {
			logx.Event("resuming %s after usage-limit cooldown elapsed", role)
		}

		if circuitOpen {
			logx.Event("skipping %s because circuit breaker is open for %s", role, task.Title)
			continue
		}

		if exists || alreadyFinished {
			continue
		}

		if paused, until := codexLaunchPaused(time.Now()); paused {
			logx.Event("skipping %s because provider usage limit pause is active until %s", role, until.Format(time.RFC3339))
			continue
		}

		attempt := 1
		if lease.LeaseID == leaseID {
			attempt = lease.Attempt
			if lease.Terminal == nil || (lease.Terminal.Outcome != string(sessionCancelled) && lease.Terminal.Outcome != string(sessionUsageLimited)) {
				attempt++
			}
		}
		newLease := ControllerWorkerLease{
			Intent:  ControllerTaskIntent{TaskID: controllerTaskID(task, taskKey), Role: role, Branch: controllerBranch(task, role)},
			LeaseID: leaseID, HeartbeatAt: time.Now(), Attempt: attempt,
		}
		if err := commitRecoveryLease(durable, newLease, taskKey); err != nil {
			logx.Event("cannot record %s lease; refusing launch: %v", role, err)
			continue
		}
		durable, err = loadControllerState()
		if err != nil {
			logx.Event("cannot reload %s lease; refusing launch: %v", role, err)
			continue
		}
		launchControllerSession(role, task, taskKey, tasks, leaseID)
		if role == teamLeadRole {
			mu.Lock()
			lastTeamLeadActivity = time.Now()
			mu.Unlock()
		}
	}
}

// completionFollowUpTask snapshots completed or failed dev sessions and turns
// the snapshot into a stable synthetic Team Lead task. The key changes only when
// a dev session ends or its outcome changes, which naturally debounces the pass.
func completionFollowUpTask() (board.Task, bool) {
	mu.Lock()
	defer mu.Unlock()

	entries := make([]string, 0, len(finished))
	for role, session := range finished {
		if _, isDevAgent := config.DevAgentIndexForRole(role); !isDevAgent {
			continue
		}
		if session.Outcome != sessionCompleted && session.Outcome != sessionFailed {
			continue
		}
		entries = append(entries, role+" | "+string(session.Outcome)+" | "+session.Task+" | "+session.TaskKey)
	}
	if len(entries) == 0 {
		return board.Task{}, false
	}
	sort.Strings(entries)

	body := `Dev-agent sessions have ended. Perform a board-only completion follow-up before assigning new work.

For every session listed below, inspect its task's current board state and choose exactly one outcome:
1. Merged and archived — verify the squash commit is on the base branch, then archive as Done.
2. Ready to merge — if tests pass, you may rebase, squash-merge, and push to the base branch yourself, then archive. If you direct the owning dev agent to rebase or push instead, you MUST run: slopboss board resume TASK-ID --reason "specific instructions". Do NOT write markdown notes directing the agent in TASKS.md — finished agents will not run without board resume.
3. Blocked — record the exact failing command and blocker, and move the task out of the lane.
4. Requeued — move back to backlog with corrected scope and dependencies.

A completed session is not proof of a merge. Do not modify product code or inspect unrelated branches.

Ended sessions:
- ` + strings.Join(entries, "\n- ")

	return board.Task{
		Section: teamLeadFollowUpSection,
		Title:   "Reconcile ended dev-agent sessions",
		Status:  "In Progress",
		Body:    body,
		Key:     "follow-up\x00" + strings.Join(entries, "\x00"),
	}, true
}

func teamLeadScheduleKey(task board.Task, tasks []board.Task, done map[string]bool) string {
	activeLaneKeys := make([]string, 0, config.DevAgentCount)
	for _, task := range tasks {
		if role, ok := config.DevAgentRoleForActiveSection(task.Section); ok &&
			task.Owner == role &&
			strings.EqualFold(task.Status, "In Progress") {
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

func idleAuditTask(now time.Time) board.Task {
	window := now.Unix() / int64(teamLeadIdleAuditInterval.Seconds())
	return board.Task{
		Section: teamLeadIdleAuditSection,
		Title:   "Periodic board health review",
		Status:  "In Progress",
		Body:    idleAuditBody,
		Key:     "idle-audit\x00" + strconv.FormatInt(window, 10),
	}
}

// canRetryFailedSessionLocked checks cooldown; durable leases limit attempts.
func canRetryFailedSessionLocked(role string, session FinishedSession, now time.Time) bool {
	retryablePass := retryableCompletedTeamLeadPass(role, session.TaskKey, string(session.Outcome))
	return (session.Outcome == sessionFailed || retryablePass) && now.Sub(session.FinishedAt) >= config.FailedSessionRetryDelay
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

func pauseLaunchesAfterUsageLimit(now time.Time) time.Time {
	until := now.Add(config.UsageLimitCooldown)

	mu.Lock()
	if until.After(codexPausedUntil) {
		codexPausedUntil = until
	}
	activeUntil := codexPausedUntil
	mu.Unlock()

	logx.Event("pausing launches after usage-limit error until %s", activeUntil.Format(time.RFC3339))
	return activeUntil
}

// circuitKey returns the map key for a task's circuit breaker.
func circuitKey(role, taskKey string) string {
	return role + "\x00" + taskKey
}

// circuitOpenLocked reports whether the circuit for key is currently open (i.e.
// the task should not be retried yet). It transitions half-open circuits to
// allow a single probe attempt once the cooldown has elapsed. Caller must hold mu.
func circuitOpenLocked(key string, now time.Time) bool {
	cb, ok := circuitBreakers[key]
	if !ok || cb == nil {
		return false
	}
	switch cb.state {
	case circuitClosed:
		return false
	case circuitOpen:
		if now.Sub(cb.openedAt) >= cb.cooldown {
			cb.state = circuitHalfOpen
			return false
		}
		return true
	case circuitHalfOpen:
		return false
	}
	return false
}

// recordCircuitResultLocked updates the circuit breaker for key based on the
// outcome of a session. Caller must hold mu.
func recordCircuitResultLocked(key string, success bool, now time.Time) {
	cb, ok := circuitBreakers[key]
	if !ok || cb == nil {
		cb = &circuitBreaker{state: circuitClosed}
		circuitBreakers[key] = cb
	}
	if success {
		cb.failures = 0
		cb.state = circuitClosed
		cb.cooldown = 0
		return
	}
	cb.failures++
	if cb.failures >= circuitThreshold {
		cb.state = circuitOpen
		cb.openedAt = now
		if cb.cooldown == 0 {
			cb.cooldown = circuitBaseDelay
		} else {
			cb.cooldown *= 2
			if cb.cooldown > circuitMaxDelay {
				cb.cooldown = circuitMaxDelay
			}
		}
		logx.Event("circuit breaker opened for %s after %d failures; cooldown %s", key, cb.failures, cb.cooldown)
	}
}
