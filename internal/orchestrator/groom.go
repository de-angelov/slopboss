package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/git"
	"github.com/de-angelov/slopboss/internal/prompt"
	"github.com/de-angelov/slopboss/internal/provider"
)

// Groom launches an interactive Team Lead session preloaded with its instructions
// and the current board, ready to capture and prioritize new tasks in
// BACKLOG.md. This is a one-off grooming session, separate from the autonomous
// run loop.
func Groom(ctx context.Context, p provider.Provider) error {
	if !git.WorkspaceExists(teamLeadRole) {
		return fmt.Errorf("team lead workspace missing (%s); run 'slopboss setup' first", config.TeamLeadPath)
	}

	tasks, err := board.ReadBoardTasks()
	if err != nil {
		return fmt.Errorf("failed to read board files: %w", err)
	}

	promptText := buildGroomPrompt(tasks)
	model := p.DefaultModel(teamLeadRole)

	cmd := p.InteractiveCommand(ctx, model, promptText)
	cmd.Dir = config.TeamLeadPath
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Starting %s Team Lead grooming session in %s\n", p.Name(), config.TeamLeadPath)
	return cmd.Run()
}

// buildGroomPrompt seeds an interactive Team Lead grooming session with the same
// role instructions and board context the loop's Team Lead receives, framed for
// interactive backlog grooming rather than an assigned task.
func buildGroomPrompt(tasks []board.Task) string {
	common := board.MustRead(config.AgentsFile)
	teamLead := board.MustRead(config.TlAgentInstructionsFile)
	tech := board.MustRead(config.TechFile)
	boardContext := prompt.BuildTaskContext(teamLeadRole, board.Task{}, tasks)
	humanDecisionSummary := buildHumanDecisionSummary(tasks)

	return fmt.Sprintf(`You are the Team Lead agent in an INTERACTIVE backlog grooming session.

================ AGENTS.md COMMON RULES ================

%s

================ TEAM LEAD INSTRUCTIONS ================

%s

================ TECH.md ================

%s

================ HUMAN DECISIONS WAITING ================

%s

================ CURRENT BOARD ================

%s

================ GROOMING SESSION ================

Work interactively with the user to groom the backlog:
- Capture new tasks into BACKLOG.md using the standard task format (Title, Owner,
  Branch, Status, and the task body sections).
- Reprioritize, split, or clarify backlog items as the user requests.
- Do NOT implement code, and do NOT modify the Dev Agent lanes in TASKS.md.
- Begin by asking the user what they would like to add or reprioritize.
`, common, teamLead, tech, humanDecisionSummary, boardContext)
}

func buildHumanDecisionSummary(tasks []board.Task) string {
	var waiting []board.Task
	for _, task := range tasks {
		if task.Section != "Backlog" {
			continue
		}
		if !(task.Status == "Backlog" || task.Status == "" || strings.EqualFold(task.Status, "Blocked")) {
			continue
		}
		if !strings.EqualFold(task.Category, "HITL") {
			continue
		}
		waiting = append(waiting, task)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d task(s) awaiting human decision.\n", len(waiting))
	for _, task := range waiting {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			id = "no-id"
		}
		fmt.Fprintf(&b, "- %s - %s\n", id, task.Title)
	}
	return strings.TrimSpace(b.String())
}
