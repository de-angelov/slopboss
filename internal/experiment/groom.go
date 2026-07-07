package experiment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/de-angelov/slopboss/internal/board"
	cfg "github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
	"github.com/de-angelov/slopboss/internal/prompt"
	"github.com/de-angelov/slopboss/internal/provider"
)

// ExperimentFilePath is the default location of the Markdown experiment spec: the
// repo root, alongside the board files.
func ExperimentFilePath() string {
	return filepath.Join(cfg.RepoRoot, ExperimentFileName)
}

// Groom launches an interactive Team Lead session, preloaded with the agent
// instructions and current board, to help the user author an experiment in
// EXPERIMENT.md. It mirrors the backlog grooming flow but targets the experiment
// spec instead of BACKLOG.md, and is separate from "experiment run".
func Groom(ctx context.Context, p provider.Provider) error {
	if !git.WorkspaceExists(cfg.TeamLeadRole) {
		return fmt.Errorf("team lead workspace missing (%s); run 'slopboss setup' first", cfg.TeamLeadPath)
	}

	tasks, err := board.ReadBoardTasks()
	if err != nil {
		return fmt.Errorf("failed to read board files: %w", err)
	}

	promptText := buildExperimentGroomPrompt(tasks)
	model := p.DefaultModel(cfg.TeamLeadRole)

	// Run at the repo root so the agent writes EXPERIMENT.md alongside the other
	// board files (BACKLOG.md, TASKS.md, …), and can inspect ./workspaces/repo-tl
	// for product context. This is where "experiment run --config EXPERIMENT.md"
	// looks for it.
	cmd := p.InteractiveCommand(ctx, model, promptText)
	cmd.Dir = cfg.RepoRoot
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Starting %s experiment grooming session in %s\n", p.Name(), cfg.RepoRoot)
	fmt.Printf("The Team Lead will write %s; then run it with:\n  slopboss experiment run --config %s\n",
		ExperimentFilePath(), ExperimentFileName)
	return cmd.Run()
}

// buildExperimentGroomPrompt seeds an interactive Team Lead session with the same
// role instructions and board context grooming receives, framed for authoring an
// experiment spec in the Markdown format the runner understands.
func buildExperimentGroomPrompt(tasks []board.Task) string {
	common := board.MustRead(cfg.AgentsFile)
	teamLead := board.MustRead(cfg.TlAgentInstructionsFile)
	tech := board.MustRead(cfg.TechFile)
	boardContext := prompt.BuildTaskContext(cfg.TeamLeadRole, board.Task{}, tasks)

	return fmt.Sprintf(`You are the Team Lead agent in an INTERACTIVE experiment-design session.

================ AGENTS.md COMMON RULES ================

%s

================ TEAM LEAD INSTRUCTIONS ================

%s

================ TECH.md ================

%s

================ CURRENT BOARD ================

%s

================ EXPERIMENT FILE FORMAT ================

Your current working directory is the repository root — it already contains the
board files (BACKLOG.md, TASKS.md, ARCHIVE.md, AGENTS.md, TECH.md, …) and a
workspaces/ directory. Write the experiment to ./%s HERE, alongside those board
files (do NOT put it under workspaces/). Use EXACTLY this Markdown format
(structured settings are "- Key: Value" bullets; prose is allowed and ignored by
the parser):

%s

================ EXPERIMENT DESIGN SESSION ================

Work interactively with the user to design a model/prompt/backend experiment and
capture it in ./%s:
- Pick the task to test: reference an existing backlog task by its exact title
  (Task:) or point at a ticket file (Ticket:). Do NOT invent work not on the board
  without confirming with the user. You MAY read ./workspaces/repo-tl for product
  context, but do not modify anything under workspaces/.
- Propose 2+ variants that isolate ONE difference each (e.g. codex vs claude, or
  two models, or two prompt files) so the results are comparable.
- Only set codex-only fields (Profile, Config) on codex variants.
- Do NOT run the experiment or implement the task; only write ./%s.
- Begin by asking the user which task they want to test and what they want to
  compare.
`, common, teamLead, tech, boardContext, ExperimentFileName, MarkdownFormatSpec, ExperimentFileName, ExperimentFileName)
}
