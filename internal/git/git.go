// Package git wraps the git plumbing slopboss needs to prepare per-agent
// workspaces, checkpoint dirty trees, and inspect origin/main. It shells out to
// the git CLI and tees command output to the shared log.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/logx"
)

// WorkspaceExists reports whether the workspace directory for role is present.
func WorkspaceExists(role string) bool {
	var path string
	switch {
	case role == config.TeamLeadRole:
		path = config.TeamLeadPath
	default:
		idx, ok := config.DevAgentIndexForRole(role)
		if !ok {
			return false
		}
		path = config.DevAgentWorkspace(idx)
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func CurrentBranchName(workspace string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workspace
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func RunGit(workspace string, args ...string) {
	logx.Event("git %s [%s]", strings.Join(args, " "), workspace)

	cmd := exec.Command("git", args...)
	cmd.Dir = workspace

	cmd.Stdout = logx.Writer{}
	cmd.Stderr = logx.Writer{}

	if err := cmd.Run(); err != nil {
		logx.Event("git failed in %s: %v", workspace, err)
	}
}

func PrepareBranch(workspace string, branch string) error {
	if err := RunGitChecked(workspace, "fetch", "--all", "--prune"); err != nil {
		return err
	}
	if err := pruneStaleWorktrees(workspace); err != nil {
		return err
	}

	currentBranch := CurrentBranchName(workspace)
	if currentBranch == branch {
		logx.Event("workspace %s already on %s; preserving local work", workspace, branch)
		return nil
	}

	if err := checkpointDirtyWorkspace(workspace, branch); err != nil {
		return err
	}

	if branchExists(workspace, branch) {
		if err := RunGitChecked(workspace, "checkout", branch); err != nil {
			return err
		}
		if remoteBranchExists(workspace, "origin/"+branch) {
			return RunGitChecked(workspace, "pull", "--rebase", "origin", branch)
		}
		return RunGitChecked(workspace, "push", "-u", "origin", branch)
	}

	remoteRef := "origin/" + branch
	if remoteBranchExists(workspace, remoteRef) {
		return RunGitChecked(workspace, "checkout", "-B", branch, remoteRef)
	}

	if err := RunGitChecked(workspace, "checkout", "main"); err != nil {
		return err
	}
	if err := RunGitChecked(workspace, "pull", "--rebase", "origin", "main"); err != nil {
		return err
	}
	if err := RunGitChecked(workspace, "checkout", "-b", branch); err != nil {
		return err
	}
	return RunGitChecked(workspace, "push", "-u", "origin", branch)
}

func pruneStaleWorktrees(workspace string) error {
	return RunGitChecked(workspace, "worktree", "prune")
}

func checkpointDirtyWorkspace(workspace string, nextBranch string) error {
	if !WorkspaceHasChanges(workspace) && !mergeInProgress(workspace) {
		return nil
	}

	current := CurrentBranchName(workspace)
	if current == "" || current == "HEAD" {
		current = "detached"
	}

	wipBranch := fmt.Sprintf(
		"wip/orchestrator/%s/%s/%d",
		SanitizeBranchPart(current),
		SanitizeBranchPart(nextBranch),
		time.Now().Unix(),
	)

	logx.Event("checkpointing dirty workspace %s on %s before switching to %s", workspace, wipBranch, nextBranch)

	if mergeInProgress(workspace) {
		_ = RunGitChecked(workspace, "merge", "--abort")
		if mergeInProgress(workspace) {
			return fmt.Errorf("workspace has unresolved merge state; manual cleanup required before switching tasks")
		}
	}

	if err := RunGitChecked(workspace, "checkout", "-b", wipBranch); err != nil {
		return err
	}
	if err := RunGitChecked(workspace, "add", "-A"); err != nil {
		return err
	}
	if WorkspaceHasStagedChanges(workspace) {
		if err := RunGitChecked(workspace, "commit", "-m", "WIP before switching to "+nextBranch); err != nil {
			return err
		}
		if err := RunGitChecked(workspace, "push", "-u", "origin", wipBranch); err != nil {
			return err
		}
		logx.Event("saved dirty workspace to %s", wipBranch)
		return nil
	}

	logx.Event("dirty workspace resolved without commit while switching to %s", nextBranch)
	return nil
}

func WorkspaceHasChanges(workspace string) bool {
	out, err := GitOutput(workspace, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

func WorkspaceHasStagedChanges(workspace string) bool {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = workspace
	return cmd.Run() != nil
}

func mergeInProgress(workspace string) bool {
	out, err := GitOutput(workspace, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil && strings.TrimSpace(out) != ""
}

// MainCommitSubjects returns up to limit recent commit subject lines from the
// workspace's local origin/main ref, newest first. It reads the already-fetched
// ref and never fetches itself, so it adds no network I/O and cannot contend with
// an in-flight session's git operations. Returns nil on any git error (e.g. the
// ref was never fetched), so callers treat "unknown" as "nothing merged".
func MainCommitSubjects(workspace string, limit int) []string {
	out, err := GitOutput(workspace, "log", "origin/main", fmt.Sprintf("-n%d", limit), "--format=%s")
	if err != nil {
		return nil
	}

	var subjects []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			subjects = append(subjects, s)
		}
	}
	return subjects
}

func GitOutput(workspace string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workspace
	out, err := cmd.Output()
	return string(out), err
}

func SanitizeBranchPart(value string) string {
	var b strings.Builder
	lastDash := false

	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '/', r == '-', r == '_', r == '.':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}

	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "unknown"
	}

	return strconv.Itoa(len(result)) + "-" + result
}

func RunGitChecked(workspace string, args ...string) error {
	logx.Event("git %s [%s]", strings.Join(args, " "), workspace)

	cmd := exec.Command("git", args...)
	cmd.Dir = workspace

	cmd.Stdout = logx.Writer{}
	cmd.Stderr = logx.Writer{}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}

	return nil
}

func branchExists(dir string, branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branch)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func remoteBranchExists(dir string, ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "refs/remotes/"+ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}
