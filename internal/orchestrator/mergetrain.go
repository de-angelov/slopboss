package orchestrator

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
	"github.com/de-angelov/slopboss/internal/logx"
)

var mergeTrainMu sync.Mutex

// FormatCommitMessage formats a commit message for the merge train.
// It ensures the subject starts with "TASK-ID: " and preserves all author body paragraphs.
func FormatCommitMessage(taskID, taskTitle, rawMsg string) string {
	rawMsg = strings.TrimSpace(rawMsg)
	if rawMsg == "" {
		return fmt.Sprintf("%s: %s\n\n(Auto-committed by Slopboss controller after passing verification)", taskID, taskTitle)
	}

	upperID := strings.ToUpper(strings.TrimSpace(taskID))
	upperRaw := strings.ToUpper(rawMsg)

	// Already starts with task ID
	if strings.HasPrefix(upperRaw, upperID+":") || strings.HasPrefix(upperRaw, upperID+" ") {
		return rawMsg
	}

	lines := strings.Split(rawMsg, "\n")
	first := strings.TrimSpace(lines[0])
	lines[0] = fmt.Sprintf("%s: %s", taskID, first)
	return strings.Join(lines, "\n")
}

// RunCommandInWorkspace executes a shell command string in workspace with a timeout.
func RunCommandInWorkspace(ctx context.Context, workspace, command string) error {
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
	cmd.Dir = workspace
	cmd.Stdout = logx.Writer{}
	cmd.Stderr = logx.Writer{}

	if err := cmd.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("command timed out after 10m: %s", command)
		}
		return fmt.Errorf("command failed: %s (%w)", command, err)
	}
	return nil
}

// LandCompletedTask executes the hybrid merge train pipeline on a completed dev agent workspace.
func LandCompletedTask(ctx context.Context, task board.Task, role, workspace string) (SessionOutcome, string) {
	if task.ID == "" || task.Branch == "" {
		return sessionFailed, "task is missing ID or branch"
	}

	mergeTrainMu.Lock()
	defer mergeTrainMu.Unlock()

	logx.Event("merge train: evaluating %s on branch %s (%s)", task.ID, task.Branch, role)

	verificationCmd := board.ExtractVerification(task.Body)

	// Step 1: Check if workspace has uncommitted changes.
	if git.WorkspaceHasChanges(workspace) {
		if verificationCmd == "" {
			return sessionFailed, "workspace has uncommitted changes but task has no verification command"
		}
		logx.Event("merge train: running verification on dirty workspace for %s: %s", task.ID, verificationCmd)
		if err := RunCommandInWorkspace(ctx, workspace, verificationCmd); err != nil {
			return sessionFailed, fmt.Sprintf("uncommitted changes failed verification: %v", err)
		}
		fallbackMsg := FormatCommitMessage(task.ID, task.Title, "")
		if err := git.CommitAll(workspace, fallbackMsg); err != nil {
			return sessionFailed, fmt.Sprintf("failed to auto-commit verified changes: %v", err)
		}
		logx.Event("merge train: auto-committed dirty changes for %s after verification passed", task.ID)
	}

	// Step 2: Check if branch has commits ahead of origin/baseBranch.
	ahead, err := git.CommitsAheadOfBase(workspace, task.Branch, config.BaseBranch)
	if err != nil {
		return sessionFailed, fmt.Sprintf("failed to determine commits ahead of origin/%s: %v", config.BaseBranch, err)
	}
	if ahead == 0 {
		// Zero-commit task (e.g. audit, investigation, or characterization where findings
		// are recorded in the task prose or no code diff was required).
		if verificationCmd != "" {
			logx.Event("merge train: running verification for zero-commit task %s: %s", task.ID, verificationCmd)
			if err := RunCommandInWorkspace(ctx, workspace, verificationCmd); err != nil {
				return sessionFailed, fmt.Sprintf("zero-commit task %s failed verification: %v", task.ID, err)
			}
		}
		today := time.Now().Format("2006-01-02")
		_, err = board.Set(task.ID, board.Update{
			File:      "archive",
			Section:   "Done",
			Status:    "Done",
			Completed: today,
		})
		if err != nil {
			return sessionFailed, fmt.Sprintf("failed to archive zero-commit task %s: %v", task.ID, err)
		}
		logx.Event("merge train: completed zero-commit task %s and moved to archive", task.ID)
		return sessionCompleted, "archived zero-commit task (audit/verification passed)"
	}

	// Step 3: Extract author commit message.
	rawMsg, _ := git.LastCommitMessage(workspace, task.Branch)
	commitMsg := FormatCommitMessage(task.ID, task.Title, rawMsg)

	// Step 4: Fetch origin and rebase onto origin/baseBranch.
	if err := git.FetchBaseBranch(workspace); err != nil {
		return sessionFailed, fmt.Sprintf("failed to fetch origin/%s: %v", config.BaseBranch, err)
	}
	if err := git.RebaseOnto(workspace, "origin/"+config.BaseBranch); err != nil {
		return sessionFailed, fmt.Sprintf("rebase onto origin/%s failed: %v", config.BaseBranch, err)
	}

	// Step 5: Verification after rebase.
	if verificationCmd != "" {
		logx.Event("merge train: verifying rebased branch for %s: %s", task.ID, verificationCmd)
		if err := RunCommandInWorkspace(ctx, workspace, verificationCmd); err != nil {
			return sessionFailed, fmt.Sprintf("verification failed after rebase onto origin/%s: %v", config.BaseBranch, err)
		}
	}

	// Step 6: Push task branch and squash-merge to base branch.
	_ = git.RunGitChecked(workspace, "push", "-u", "origin", task.Branch, "--force-with-lease")

	commitHash, err := git.SquashMergeAndPush(workspace, task.Branch, config.BaseBranch, commitMsg)
	if err != nil {
		return sessionFailed, fmt.Sprintf("squash merge or push to %s failed: %v", config.BaseBranch, err)
	}

	// Step 7: Update board to archive.
	today := time.Now().Format("2006-01-02")
	_, err = board.Set(task.ID, board.Update{
		File:      "archive",
		Section:   "Done",
		Status:    "Done",
		Completed: today,
	})
	if err != nil {
		logx.Event("merge train: landed on main (%s) but failed to update archive for %s: %v", commitHash, task.ID, err)
		return sessionCompleted, fmt.Sprintf("landed commit %s on origin/%s (archive update warning: %v)", commitHash, config.BaseBranch, err)
	}

	logx.Event("merge train: successfully landed %s on %s (%s) and moved to archive", task.ID, config.BaseBranch, commitHash)
	return sessionCompleted, fmt.Sprintf("landed commit %s on origin/%s", commitHash, config.BaseBranch)
}
