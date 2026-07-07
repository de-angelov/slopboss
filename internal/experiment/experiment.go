// Package experiment runs model/prompt A/B experiments: for each variant it
// prepares an isolated git worktree, runs an agent backend against a ticket
// prompt, and collects token/diff metrics into a JSON + Markdown report. It drives
// any backend through the provider abstraction (codex or claude), so variants can
// even mix backends within a single run.
package experiment

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	cfg "github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
	"github.com/de-angelov/slopboss/internal/prompt"
	"github.com/de-angelov/slopboss/internal/provider"
)

type ExperimentConfig struct {
	Name                  string              `json:"name"`
	Provider              string              `json:"provider"`
	SourceWorkspace       string              `json:"sourceWorkspace"`
	BaseBranch            string              `json:"baseBranch"`
	TicketFile            string              `json:"ticketFile"`
	TaskSourceFile        string              `json:"taskSourceFile"`
	TaskTitle             string              `json:"taskTitle"`
	PromptMode            string              `json:"promptMode"`
	PromptRole            string              `json:"promptRole"`
	PromptBranch          string              `json:"promptBranch"`
	OutputDir             string              `json:"outputDir"`
	TimeoutMinutes        int                 `json:"timeoutMinutes"`
	PrepareTimeoutMinutes int                 `json:"prepareTimeoutMinutes"`
	PrepareCommands       []string            `json:"prepareCommands"`
	SkipPrepare           bool                `json:"skipPrepare"`
	Variants              []ExperimentVariant `json:"variants"`
}

type ExperimentVariant struct {
	Name       string            `json:"name"`
	Provider   string            `json:"provider"`
	Model      string            `json:"model"`
	Profile    string            `json:"profile"`
	PromptFile string            `json:"promptFile"`
	Config     map[string]string `json:"config"`
}

type ExperimentRun struct {
	Name       string                    `json:"name"`
	StartedAt  time.Time                 `json:"startedAt"`
	FinishedAt time.Time                 `json:"finishedAt"`
	BaseBranch string                    `json:"baseBranch"`
	BaseCommit string                    `json:"baseCommit"`
	TicketFile string                    `json:"ticketFile"`
	Results    []ExperimentVariantResult `json:"results"`
}

type ExperimentVariantResult struct {
	Name                 string            `json:"name"`
	Provider             string            `json:"provider,omitempty"`
	Model                string            `json:"model,omitempty"`
	Profile              string            `json:"profile,omitempty"`
	Config               map[string]string `json:"config,omitempty"`
	Branch               string            `json:"branch"`
	Worktree             string            `json:"worktree"`
	LogFile              string            `json:"logFile"`
	PrepareLogFile       string            `json:"prepareLogFile,omitempty"`
	LastMessageFile      string            `json:"lastMessageFile"`
	PatchFile            string            `json:"patchFile"`
	Status               string            `json:"status"`
	PrepareStatus        string            `json:"prepareStatus,omitempty"`
	PrepareError         string            `json:"prepareError,omitempty"`
	PrepareCommands      []string          `json:"prepareCommands,omitempty"`
	PrepareMilliseconds  int64             `json:"prepareMilliseconds,omitempty"`
	ExitError            string            `json:"exitError,omitempty"`
	StartedAt            time.Time         `json:"startedAt"`
	FinishedAt           time.Time         `json:"finishedAt"`
	DurationMilliseconds int64             `json:"durationMilliseconds"`
	PromptBytes          int               `json:"promptBytes"`
	ApproxPromptTokens   int               `json:"approxPromptTokens"`
	DetectedInputTokens  int               `json:"detectedInputTokens,omitempty"`
	DetectedOutputTokens int               `json:"detectedOutputTokens,omitempty"`
	DetectedTotalTokens  int               `json:"detectedTotalTokens,omitempty"`
	BaseCommit           string            `json:"baseCommit"`
	HeadCommit           string            `json:"headCommit"`
	CommitCount          int               `json:"commitCount"`
	ChangedFiles         int               `json:"changedFiles"`
	Insertions           int               `json:"insertions"`
	Deletions            int               `json:"deletions"`
	DirtyAfterRun        bool              `json:"dirtyAfterRun"`
	AutoCommittedChanges bool              `json:"autoCommittedChanges"`
	FinalResponseSummary string            `json:"finalResponseSummary,omitempty"`
}

// ReadConfig loads and validates an experiment config, applying defaults. A
// ".md" path is parsed as the Markdown experiment format; anything else is parsed
// as JSON.
func ReadConfig(path string) (ExperimentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ExperimentConfig{}, err
	}

	var config ExperimentConfig
	if strings.HasSuffix(strings.ToLower(path), ".md") {
		config, err = parseMarkdownConfig(string(data))
	} else {
		err = json.Unmarshal(data, &config)
	}
	if err != nil {
		return ExperimentConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if config.Name == "" {
		return ExperimentConfig{}, fmt.Errorf("name is required")
	}
	if config.Provider != "" {
		if _, err := provider.ByName(config.Provider); err != nil {
			return ExperimentConfig{}, err
		}
	}
	if config.SourceWorkspace == "" {
		config.SourceWorkspace = cfg.Agent1Path
	}
	if !filepath.IsAbs(config.SourceWorkspace) {
		config.SourceWorkspace = filepath.Join(cfg.RepoRoot, config.SourceWorkspace)
	}
	if config.BaseBranch == "" {
		config.BaseBranch = git.CurrentBranchName(config.SourceWorkspace)
		if config.BaseBranch == "" {
			config.BaseBranch = "main"
		}
	}
	if config.TicketFile != "" && !filepath.IsAbs(config.TicketFile) {
		config.TicketFile = filepath.Join(cfg.RepoRoot, config.TicketFile)
	}
	if config.TaskSourceFile != "" && !filepath.IsAbs(config.TaskSourceFile) {
		config.TaskSourceFile = filepath.Join(cfg.RepoRoot, config.TaskSourceFile)
	}
	if config.TicketFile == "" && config.TaskTitle == "" {
		return ExperimentConfig{}, fmt.Errorf("ticketFile or taskTitle is required")
	}
	if config.TicketFile != "" && config.TaskTitle != "" {
		return ExperimentConfig{}, fmt.Errorf("use either ticketFile or taskTitle, not both")
	}
	if config.PromptMode == "" {
		config.PromptMode = "bounded"
	}
	if config.PromptMode != "bounded" && config.PromptMode != "orchestrator-dev" {
		return ExperimentConfig{}, fmt.Errorf("unsupported promptMode %q", config.PromptMode)
	}
	if config.PromptRole == "" {
		config.PromptRole = cfg.DevAgent1Role
	}
	if config.PromptMode == "orchestrator-dev" && config.PromptRole != cfg.DevAgent1Role && config.PromptRole != cfg.DevAgent2Role {
		return ExperimentConfig{}, fmt.Errorf("orchestrator-dev promptRole must be %q or %q", cfg.DevAgent1Role, cfg.DevAgent2Role)
	}
	if config.OutputDir == "" {
		config.OutputDir = filepath.Join(cfg.RepoRoot, "evals")
	} else if !filepath.IsAbs(config.OutputDir) {
		config.OutputDir = filepath.Join(cfg.RepoRoot, config.OutputDir)
	}
	if config.TimeoutMinutes == 0 {
		config.TimeoutMinutes = 90
	}
	if config.PrepareTimeoutMinutes == 0 {
		config.PrepareTimeoutMinutes = 20
	}
	if len(config.Variants) == 0 {
		return ExperimentConfig{}, fmt.Errorf("at least one variant is required")
	}
	seen := map[string]bool{}
	for i := range config.Variants {
		if config.Variants[i].Name == "" {
			return ExperimentConfig{}, fmt.Errorf("variants[%d].name is required", i)
		}
		if seen[config.Variants[i].Name] {
			return ExperimentConfig{}, fmt.Errorf("duplicate variant name %q", config.Variants[i].Name)
		}
		seen[config.Variants[i].Name] = true
		if config.Variants[i].Provider != "" {
			if _, err := provider.ByName(config.Variants[i].Provider); err != nil {
				return ExperimentConfig{}, fmt.Errorf("variants[%d]: %w", i, err)
			}
		}
		if config.Variants[i].PromptFile != "" && !filepath.IsAbs(config.Variants[i].PromptFile) {
			config.Variants[i].PromptFile = filepath.Join(cfg.RepoRoot, config.Variants[i].PromptFile)
		}
	}

	return config, nil
}

// variantProvider resolves the backend for one variant: an explicit per-variant
// provider wins, then the config-level default, then the run-level default (the
// experiment command's --provider flag).
func variantProvider(config ExperimentConfig, variant ExperimentVariant, defaultProvider string) (provider.Provider, string, error) {
	name := variant.Provider
	if name == "" {
		name = config.Provider
	}
	if name == "" {
		name = defaultProvider
	}
	p, err := provider.ByName(name)
	if err != nil {
		return nil, "", err
	}
	return p, p.Name(), nil
}

func (config ExperimentConfig) ResolvedOutputDir() string {
	if config.OutputDir == "" {
		return filepath.Join(cfg.RepoRoot, "evals")
	}
	return config.OutputDir
}

// Run executes every variant of config and writes the run report. defaultProvider
// is the run-level backend (the experiment command's --provider flag); individual
// variants or the config may override it.
func Run(ctx context.Context, config ExperimentConfig, defaultProvider string, dryRun bool) (ExperimentRun, error) {
	runName := experimentRunName(config.Name, time.Now())
	runDir := filepath.Join(config.ResolvedOutputDir(), runName)
	worktreeRoot := filepath.Join(runDir, "worktrees")
	if err := os.MkdirAll(worktreeRoot, 0755); err != nil {
		return ExperimentRun{}, err
	}

	baseCommit, err := git.GitOutput(config.SourceWorkspace, "rev-parse", config.BaseBranch)
	if err != nil {
		return ExperimentRun{}, fmt.Errorf("resolve base branch %s: %w", config.BaseBranch, err)
	}
	baseCommit = strings.TrimSpace(baseCommit)

	task, ticketSource, err := resolveExperimentTask(config)
	if err != nil {
		return ExperimentRun{}, err
	}

	run := ExperimentRun{
		Name:       runName,
		StartedAt:  time.Now(),
		BaseBranch: config.BaseBranch,
		BaseCommit: baseCommit,
		TicketFile: ticketSource,
	}

	for _, variant := range config.Variants {
		p, providerName, err := variantProvider(config, variant, defaultProvider)
		if err != nil {
			return run, fmt.Errorf("variant %q: %w", variant.Name, err)
		}
		result, err := runExperimentVariant(ctx, config, runName, worktreeRoot, baseCommit, task, variant, p, providerName, dryRun)
		if err != nil {
			return run, err
		}
		run.Results = append(run.Results, result)
	}

	run.FinishedAt = time.Now()
	if err := writeExperimentReports(filepath.Join(config.ResolvedOutputDir(), runName), run); err != nil {
		return run, err
	}
	return run, nil
}

func resolveExperimentTask(config ExperimentConfig) (board.Task, string, error) {
	if config.TicketFile != "" {
		ticket, err := os.ReadFile(config.TicketFile)
		if err != nil {
			return board.Task{}, "", err
		}
		return board.Task{
			Section: "Experiment",
			Title:   strings.TrimSuffix(filepath.Base(config.TicketFile), filepath.Ext(config.TicketFile)),
			Owner:   board.EmptyAs(config.PromptRole, cfg.DevAgent1Role),
			Branch:  config.PromptBranch,
			Status:  "In Progress",
			Body:    strings.TrimSpace(string(ticket)),
		}, config.TicketFile, nil
	}

	sourceFile := config.TaskSourceFile
	if sourceFile == "" {
		sourceFile = cfg.BacklogFile
	}
	tasks, err := board.ReadTasks(sourceFile)
	if err != nil {
		return board.Task{}, "", err
	}
	for _, task := range tasks {
		if task.Title == config.TaskTitle {
			if config.PromptMode == "orchestrator-dev" {
				task.Section = config.PromptRole + " In Progress"
				task.Owner = config.PromptRole
				task.Status = "In Progress"
				if config.PromptBranch != "" {
					task.Branch = config.PromptBranch
				}
			}
			return task, fmt.Sprintf("%s:%s", sourceFile, config.TaskTitle), nil
		}
	}
	return board.Task{}, "", fmt.Errorf("task %q not found in %s", config.TaskTitle, sourceFile)
}

func runExperimentVariant(ctx context.Context, config ExperimentConfig, runName string, worktreeRoot string, baseCommit string, task board.Task, variant ExperimentVariant, p provider.Provider, providerName string, dryRun bool) (ExperimentVariantResult, error) {
	start := time.Now()
	variantID := git.SanitizeBranchPart(variant.Name)
	branch := fmt.Sprintf("experiment/%s/%s", git.SanitizeBranchPart(runName), variantID)
	worktree := filepath.Join(worktreeRoot, variantID)
	logFile := filepath.Join(worktreeRoot, variantID+".jsonl")
	prepareLogFile := filepath.Join(worktreeRoot, variantID+"-prepare.log")
	lastMessageFile := filepath.Join(worktreeRoot, variantID+"-last-message.txt")
	patchFile := filepath.Join(worktreeRoot, variantID+".patch")

	if err := git.RunGitChecked(config.SourceWorkspace, "worktree", "add", "-B", branch, worktree, baseCommit); err != nil {
		return ExperimentVariantResult{}, err
	}

	promptText, err := buildExperimentPrompt(config, task, variant)
	if err != nil {
		return ExperimentVariantResult{}, err
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, variantID+"-prompt.txt"), []byte(promptText), 0644); err != nil {
		return ExperimentVariantResult{}, err
	}

	result := ExperimentVariantResult{
		Name:               variant.Name,
		Provider:           providerName,
		Model:              variant.Model,
		Profile:            variant.Profile,
		Config:             variant.Config,
		Branch:             branch,
		Worktree:           worktree,
		LogFile:            logFile,
		PrepareLogFile:     prepareLogFile,
		LastMessageFile:    lastMessageFile,
		PatchFile:          patchFile,
		Status:             "prepared",
		StartedAt:          start,
		PromptBytes:        len(promptText),
		ApproxPromptTokens: approximateTokens(promptText),
		BaseCommit:         baseCommit,
	}

	if dryRun {
		result.Status = "dry-run"
		result.FinishedAt = time.Now()
		result.DurationMilliseconds = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
		return finalizeExperimentResult(worktree, baseCommit, patchFile, result)
	}

	result = prepareExperimentWorktree(ctx, config, worktree, prepareLogFile, result)
	if result.PrepareStatus == "failed" {
		result.Status = "prepare-failed"
		result.FinishedAt = time.Now()
		result.DurationMilliseconds = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
		return finalizeExperimentResult(worktree, baseCommit, patchFile, result)
	}

	timeout := time.Duration(config.TimeoutMinutes) * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	usage, finalMessage, err := runVariantBackend(runCtx, p, worktree, promptText, variant, logFile, lastMessageFile)
	result.FinishedAt = time.Now()
	result.DurationMilliseconds = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	if err != nil {
		result.Status = "failed"
		result.ExitError = err.Error()
	} else {
		result.Status = "completed"
	}
	if runCtx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
		result.ExitError = runCtx.Err().Error()
	}

	result.DetectedInputTokens = usage.Input
	result.DetectedOutputTokens = usage.Output
	result.DetectedTotalTokens = usage.Total

	// Prefer the final message the monitor captured from the event stream (Claude);
	// fall back to the --output-last-message file the backend wrote (codex). Either
	// way, persist it so the artifact exists for every backend.
	if finalMessage == "" {
		if data, err := os.ReadFile(lastMessageFile); err == nil {
			finalMessage = string(data)
		}
	} else {
		_ = os.WriteFile(lastMessageFile, []byte(finalMessage), 0644)
	}
	result.FinalResponseSummary = oneLine(finalMessage)

	return finalizeExperimentResult(worktree, baseCommit, patchFile, result)
}

func prepareExperimentWorktree(ctx context.Context, config ExperimentConfig, worktree string, logFile string, result ExperimentVariantResult) ExperimentVariantResult {
	start := time.Now()
	commands := experimentPrepareCommands(config, worktree)
	result.PrepareCommands = commands

	if len(commands) == 0 {
		result.PrepareStatus = "skipped"
		return result
	}

	timeout := time.Duration(config.PrepareTimeoutMinutes) * time.Minute
	prepareCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logOutput, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		result.PrepareStatus = "failed"
		result.PrepareError = err.Error()
		return result
	}
	defer logOutput.Close()

	for _, command := range commands {
		if _, err := fmt.Fprintf(logOutput, "$ %s\n", command); err != nil {
			result.PrepareStatus = "failed"
			result.PrepareError = err.Error()
			return result
		}

		cmd := exec.CommandContext(prepareCtx, "bash", "-lc", command)
		cmd.Dir = worktree
		cmd.Stdout = logOutput
		cmd.Stderr = logOutput
		if err := cmd.Run(); err != nil {
			result.PrepareStatus = "failed"
			result.PrepareError = err.Error()
			if prepareCtx.Err() == context.DeadlineExceeded {
				result.PrepareError = prepareCtx.Err().Error()
			}
			result.PrepareMilliseconds = time.Since(start).Milliseconds()
			return result
		}
	}

	result.PrepareStatus = "completed"
	result.PrepareMilliseconds = time.Since(start).Milliseconds()
	return result
}

func experimentPrepareCommands(config ExperimentConfig, worktree string) []string {
	if config.SkipPrepare {
		return nil
	}
	if len(config.PrepareCommands) > 0 {
		return config.PrepareCommands
	}
	if _, err := os.Stat(filepath.Join(worktree, "package-lock.json")); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(worktree, "node_modules")); err == nil {
		return nil
	}

	sourceNodeModules := filepath.Join(config.SourceWorkspace, "node_modules")
	if _, err := os.Stat(sourceNodeModules); err == nil {
		return []string{fmt.Sprintf("ln -s %s node_modules", shellQuote(sourceNodeModules))}
	}

	return []string{"npm ci"}
}

func buildExperimentPrompt(config ExperimentConfig, task board.Task, variant ExperimentVariant) (string, error) {
	variantPrompt := ""
	if variant.PromptFile != "" {
		data, err := os.ReadFile(variant.PromptFile)
		if err != nil {
			return "", err
		}
		variantPrompt = string(data)
	}

	if config.PromptMode == "orchestrator-dev" {
		tasks, err := board.ReadBoardTasks()
		if err != nil {
			return "", err
		}
		built := prompt.BuildPrompt(config.PromptRole, task, tasks, `
Role: Dev Agent
Runtime Rules:
- Work only on the assigned task and keep changes focused.
`)
		return built + fmt.Sprintf(`

================ EXPERIMENT SAFETY OVERRIDES ================

- This is an isolated experiment branch, not a live orchestrator session.
- The current working directory is already the assigned product repository. Do not cd into "workspaces/repo-agent-*" unless that directory exists from the current working directory.
- Do not push, merge, update BACKLOG.md, TASKS.md, or ARCHIVE.md.
- Do not install dependencies or change package manager files unless the ticket explicitly requires it.
- Dependencies are prepared before you start.
- If verification reveals unrelated failures, stop and report them. Do not fix unrelated files.
- Do not commit; the experiment harness records and commits the final diff after the run.
%s

================ VARIANT PROMPT ================

%s
`, prompt.TestingDisciplineRules, variantPrompt), nil
	}

	return fmt.Sprintf(`You are running an isolated implementation experiment.

================ AGENTS.md COMMON RULES ================

%s

================ TECH.md ================

%s

================ BOUNDED EXPERIMENT RULES ================

- Implement the ticket in this isolated worktree only.
- The current working directory is already the assigned product repository. Do not cd into "workspaces/repo-agent-*" unless that directory exists from the current working directory.
- Do not push, merge, update BACKLOG.md, TASKS.md, or ARCHIVE.md.
- Do not install dependencies or change package manager files unless the ticket explicitly requires it; dependencies are prepared before you start.
- Use the existing repository test stack. If a missing tool or unrelated type error blocks verification, report it instead of expanding scope.
- Exploration budget: inspect at most 8 relevant files before editing unless you have a concrete failing command that requires more context.
- Implementation budget: make one focused implementation pass for this ticket.
- Verification budget: run only the ticket's listed verification command or the narrowest equivalent focused command.
%s
- If verification reveals unrelated failures, stop and report them. Do not fix unrelated files.
- Do not commit; the experiment harness records and commits the final diff after the run.
- Keep the implementation focused so this branch can be compared against other variants.

================ VARIANT PROMPT ================

%s

================ TICKET ================

%s
`, board.MustRead(cfg.AgentsFile), board.MustRead(cfg.TechFile), prompt.TestingDisciplineRules, variantPrompt, task.Body), nil
}

// runVariantBackend runs a single experiment variant through the provider
// abstraction, driving codex or claude identically. The backend's structured
// event stream is written to logFile and parsed live by the provider's Monitor,
// which yields token usage and (for backends that surface it in-stream) the final
// assistant message. codex additionally writes lastMessageFile via
// --output-last-message.
func runVariantBackend(ctx context.Context, p provider.Provider, workspace string, promptText string, variant ExperimentVariant, logFile string, lastMessageFile string) (provider.TokenBreakdown, string, error) {
	cmd := p.ExperimentCommand(ctx, provider.ExperimentSpec{
		Model:           variant.Model,
		Profile:         variant.Profile,
		Config:          variant.Config,
		LastMessageFile: lastMessageFile,
	})
	cmd.Dir = workspace
	cmd.Stdin = strings.NewReader(promptText)

	logOutput, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return provider.TokenBreakdown{}, "", err
	}
	defer logOutput.Close()

	monitor := p.NewMonitor()
	var stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(logOutput, monitor)
	cmd.Stderr = io.MultiWriter(logOutput, monitor, &stderr)

	runErr := cmd.Run()
	usage := monitor.Breakdown()
	finalMessage := monitor.FinalMessage()

	if runErr != nil {
		if stderr.Len() > 0 {
			return usage, finalMessage, fmt.Errorf("%w: %s", runErr, oneLine(stderr.String()))
		}
		return usage, finalMessage, runErr
	}
	return usage, finalMessage, nil
}

func finalizeExperimentResult(worktree string, baseCommit string, patchFile string, result ExperimentVariantResult) (ExperimentVariantResult, error) {
	if git.WorkspaceHasChanges(worktree) {
		result.DirtyAfterRun = true
		if err := stageExperimentChanges(worktree); err != nil {
			return result, err
		}
		if git.WorkspaceHasStagedChanges(worktree) {
			if err := git.RunGitChecked(worktree, "commit", "-m", "Experiment result: "+result.Name); err != nil {
				return result, err
			}
			result.AutoCommittedChanges = true
			result.DirtyAfterRun = false
		}
	}

	head, err := git.GitOutput(worktree, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	result.HeadCommit = strings.TrimSpace(head)

	count, err := git.GitOutput(worktree, "rev-list", "--count", baseCommit+"..HEAD")
	if err == nil {
		result.CommitCount, _ = strconv.Atoi(strings.TrimSpace(count))
	}

	stat, _ := git.GitOutput(worktree, "diff", "--shortstat", baseCommit+"..HEAD")
	result.ChangedFiles, result.Insertions, result.Deletions = parseDiffShortstat(stat)

	patch, err := git.GitOutput(worktree, "diff", "--patch", baseCommit+"..HEAD")
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(patchFile, []byte(patch), 0644); err != nil {
		return result, err
	}

	result.DirtyAfterRun = git.WorkspaceHasChanges(worktree)
	return result, nil
}

func stageExperimentChanges(worktree string) error {
	if err := git.RunGitChecked(worktree, "add", "-u", "--", "."); err != nil {
		return err
	}

	output, err := git.GitOutput(worktree, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}

	var paths []string
	for _, path := range strings.Split(output, "\x00") {
		if path == "" || path == "node_modules" || strings.HasPrefix(path, "node_modules/") {
			continue
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"add", "--"}, paths...)
	return git.RunGitChecked(worktree, args...)
}

func writeExperimentReports(runDir string, run ExperimentRun) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.json"), append(data, '\n'), 0644); err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Eval: %s\n\n", run.Name)
	fmt.Fprintf(&b, "- Base branch: `%s`\n", run.BaseBranch)
	fmt.Fprintf(&b, "- Base commit: `%s`\n", run.BaseCommit)
	fmt.Fprintf(&b, "- Ticket: `%s`\n", run.TicketFile)
	fmt.Fprintf(&b, "- Started: `%s`\n", run.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Finished: `%s`\n\n", run.FinishedAt.Format(time.RFC3339))

	b.WriteString("| Variant | Backend | Status | Prepare | Duration | Approx prompt tokens | Detected total tokens | Commits | Files | +/- | Branch |\n")
	b.WriteString("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, result := range run.Results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %s | %d | %d | +%d/-%d | `%s` |\n",
			escapeTable(result.Name),
			escapeTable(result.Provider),
			escapeTable(result.Status),
			escapeTable(formatPrepareStatus(result)),
			(time.Duration(result.DurationMilliseconds) * time.Millisecond).Round(time.Second),
			result.ApproxPromptTokens,
			emptyInt(result.DetectedTotalTokens),
			result.CommitCount,
			result.ChangedFiles,
			result.Insertions,
			result.Deletions,
			result.Branch,
		)
	}
	b.WriteString("\n## Artifacts\n\n")
	for _, result := range run.Results {
		fmt.Fprintf(&b, "### %s\n\n", result.Name)
		fmt.Fprintf(&b, "- Worktree: `%s`\n", result.Worktree)
		if result.PrepareLogFile != "" {
			fmt.Fprintf(&b, "- Prepare log: `%s`\n", result.PrepareLogFile)
		}
		fmt.Fprintf(&b, "- Log: `%s`\n", result.LogFile)
		fmt.Fprintf(&b, "- Patch: `%s`\n", result.PatchFile)
		fmt.Fprintf(&b, "- Head: `%s`\n", result.HeadCommit)
		if len(result.PrepareCommands) > 0 {
			fmt.Fprintf(&b, "- Prepare commands: `%s`\n", strings.Join(result.PrepareCommands, " && "))
		}
		if result.PrepareError != "" {
			fmt.Fprintf(&b, "- Prepare error: `%s`\n", result.PrepareError)
		}
		if result.ExitError != "" {
			fmt.Fprintf(&b, "- Error: `%s`\n", result.ExitError)
		}
		if result.FinalResponseSummary != "" {
			fmt.Fprintf(&b, "- Final response: %s\n", result.FinalResponseSummary)
		}
		b.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(runDir, "report.md"), []byte(b.String()), 0644)
}

func formatPrepareStatus(result ExperimentVariantResult) string {
	if result.PrepareStatus == "" {
		return ""
	}
	if result.PrepareMilliseconds == 0 {
		return result.PrepareStatus
	}
	return fmt.Sprintf("%s %s", result.PrepareStatus, (time.Duration(result.PrepareMilliseconds) * time.Millisecond).Round(time.Second))
}

func parseDiffShortstat(stat string) (int, int, int) {
	files := extractShortstatNumber(stat, `([0-9]+) files? changed`)
	insertions := extractShortstatNumber(stat, `([0-9]+) insertions?\(\+\)`)
	deletions := extractShortstatNumber(stat, `([0-9]+) deletions?\(-\)`)
	return files, insertions, deletions
}

func extractShortstatNumber(stat string, pattern string) int {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(stat)
	if len(match) < 2 {
		return 0
	}
	value, _ := strconv.Atoi(match[1])
	return value
}

func approximateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}

func experimentRunName(name string, now time.Time) string {
	return fmt.Sprintf("%s-%s-%s", git.SanitizeBranchPart(name), now.Format("20060102-150405"), shortHash(name+now.String()))
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func escapeTable(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}

func emptyInt(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// oneLine collapses whitespace and truncates long strings for report/error use.
func oneLine(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	line := strings.Join(fields, " ")
	if len(line) > 240 {
		return line[:237] + "..."
	}
	return line
}
