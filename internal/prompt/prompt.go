// Package prompt assembles the text handed to an agent backend for a session. It
// splits every prompt into a stable, cacheable prefix (role rules, AGENTS.md,
// TECH.md) and a per-run suffix (board context + active task) so the model's
// prompt cache survives across polls.
package prompt

import (
	"fmt"
	"strings"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

// TestingDisciplineRules is the shared block of dev-agent testing constraints
// injected into both the live orchestrator dev prompt and the experiment prompts
// (bounded and orchestrator-dev). It is a single const so the three call sites
// stay byte-identical — prompt-cache stability depends on identical prefixes, so
// any drift between copies would silently bust the cache. It has no leading or
// trailing newline; callers supply the surrounding whitespace.
const TestingDisciplineRules = `- Testing discipline: prefer existing test style and user-visible/rendered assertions over inspecting React component internals.
- Do not add custom test traversal helpers, mock frameworks, DOM harnesses, or new test utilities unless the ticket explicitly requires interaction behavior that cannot be covered otherwise.
- Test contract, not implementation: assert rendered text, form fields, button labels, form ids/actions, validation messages, and preserved existing behavior.
- Avoid asserting component prop wiring directly when the same behavior can be observed in rendered output.
- If the repo lacks jsdom/testing-library, do not invent an interaction harness. Use the existing SSR/render test pattern and state the interaction limit if relevant.
- Scope budget: for small route/UI tickets, keep test changes close to the changed file and avoid adding more test code than implementation code unless a failing test proves it is necessary.`

// DependencyInstallRules keeps dependency/network stalls from wedging a live dev
// lane. Package-manager installs are the main long-running command agents issue
// in greenfield repos; they must either complete, fail visibly, or turn the task
// into a blocked board item with enough evidence for a human or later unblocker.
const DependencyInstallRules = `- Dependency install discipline: run package-manager install/update commands with an explicit timeout, for example ` + "`timeout 10m npm install`, `timeout 10m pnpm install`, or the stack-equivalent command." + `
- If an install/update times out, hangs, or shows registry/network retry errors, stop retrying blindly. Clean partial install artifacts that can poison later runs (for example incomplete node_modules or package-manager temp/cache state), capture the exact failing command/output, and mark the task Blocked in TASKS.md.
- When package manifests change, commit the corresponding lockfile (` + "`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `Cargo.lock`, etc.)" + ` or explicitly block on the install failure that prevented generating it.
- Do not leave package-manager install processes running in the workspace after marking a task blocked or stopping work.`

func DevAgentRuntimeInstructions() string {
	return `
Role: Dev Agent
Runtime Rules:
- Work only on the assigned task and keep changes focused.
- The current working directory is already the assigned product repository. Do not cd into "workspaces/repo-agent-*" unless that directory exists from the current working directory.
- Use the existing repository test stack. If a missing tool or unrelated type error blocks verification, report it instead of expanding scope.
- If verification reveals unrelated failures, stop and mark the task Blocked in TASKS.md. Do not fix unrelated files.
- For unrelated verification failures, add or update a [BLOCKED] section with the failing command, exact output, out-of-scope file path, and a suggested narrow unblocker task. Set Status: Blocked and Blocked by: Team Lead triage required unless a blocker task already exists.
` + DependencyInstallRules + "\n" + TestingDisciplineRules + "\n"
}

// BuildPrompt assembles a full session prompt for role/task. The static prefix is
// identical for every session of a given role (cacheable); only the board context
// and active task vary per run.
func BuildPrompt(role string, task board.Task, tasks []board.Task, roleInstructions string) string {
	commonInstructions := board.MustRead(config.AgentsFile)
	specificInstructions := board.MustRead(roleInstructionsPath(role))
	tech := board.MustRead(config.TechFile)
	taskContext := BuildTaskContext(role, task, tasks)

	// The prompt is split into a stable prefix and a per-run suffix so the model's
	// prompt cache can be reused across sessions. Everything above the "PER-RUN
	// CONTEXT" divider is identical for every session of a given role (role name,
	// AGENTS.md, role instructions, TECH.md, runtime rules), so it forms a
	// cacheable prefix. Only the board context and the active task — which change
	// each tick — appear after the divider. Do NOT move tick-varying content above
	// the divider or interleave it, as that breaks prefix caching and re-bills the
	// static context on every rerun/retry.
	staticPrefix := fmt.Sprintf(`
You are running inside the multi-agent development workflow.

Active role: %s

================ AGENTS.md COMMON RULES ================

%s

================ ROLE-SPECIFIC INSTRUCTIONS ================

%s

================ TECH.md ================

%s

================ RUNTIME INSTRUCTIONS ================

%s
`, role, commonInstructions, specificInstructions, tech, roleInstructions)

	perRunSuffix := fmt.Sprintf(`
================ PER-RUN CONTEXT (changes every session) ================

================ BOARD CONTEXT ================

%s

================ ACTIVE TASK ================

Section: %s
Title: %s
Owner: %s
Branch: %s
Status: %s

Task body:

%s
`, taskContext, task.Section, task.Title, task.Owner, task.Branch, task.Status, task.Body)

	return staticPrefix + perRunSuffix
}

func roleInstructionsPath(role string) string {
	if role == config.TeamLeadRole {
		return config.TlAgentInstructionsFile
	}
	return config.DevAgentInstructionsFile
}

// BuildTaskContext renders the compact, token-lean board summary injected into a
// session for the given role.
func BuildTaskContext(role string, activeTask board.Task, tasks []board.Task) string {
	var b strings.Builder
	b.WriteString("Active task body is shown separately below. This context is summarized to save tokens.\n")

	switch role {
	case config.TeamLeadRole:
		b.WriteString("\nBacklog:\n")
		writeTaskSummaries(&b, tasks, func(task board.Task) bool {
			return task.Section == "Backlog" && (task.Status == "Backlog" || task.Status == "")
		})

		b.WriteString("\nDev-agent lanes:\n")
		writeTaskSummaries(&b, tasks, func(task board.Task) bool {
			return strings.HasSuffix(task.Section, "In Progress")
		})

		// The board parser skips the archive's large Done section to save tokens,
		// so completed work is invisible to the summaries above. Grooming needs it
		// to resolve dependencies ("is this task's dependency Done?"), so surface
		// the completed work as a compact ID list — IDs only, never bodies. This is
		// the union of ARCHIVE.md and work already squash-merged to origin/main, so
		// the team lead reasons about readiness from the same completed set the
		// scheduler uses — a task merged but not yet archived still reads as done.
		b.WriteString("\nCompleted task IDs (Done in ARCHIVE.md or already merged to main; a task whose dependencies are all here is unblocked):\n")
		if ids := board.CompletedContextIDs(); len(ids) > 0 {
			fmt.Fprintf(&b, "- %s\n", strings.Join(ids, ", "))
		} else {
			b.WriteString("- none\n")
		}

	default:
		b.WriteString("\nOther active dev-agent work:\n")
		writeTaskSummaries(&b, tasks, func(task board.Task) bool {
			if task.Title == activeTask.Title &&
				task.Owner == activeTask.Owner &&
				task.Branch == activeTask.Branch {
				return false
			}
			return strings.HasSuffix(task.Section, "In Progress")
		})
	}

	return strings.TrimSpace(b.String())
}

func writeTaskSummaries(b *strings.Builder, tasks []board.Task, include func(board.Task) bool) {
	wrote := false
	for _, task := range tasks {
		if !include(task) {
			continue
		}

		wrote = true
		fmt.Fprintf(b, "- %s [%s] | Status: %s | Category: %s | Owner: %s | Branch: %s | Deps: %s | Blocks: %s | %s\n",
			task.Title,
			board.EmptyAs(task.ID, "?"),
			board.EmptyAs(task.Status, "(none)"),
			board.EmptyAs(task.Category, "-"),
			board.EmptyAs(task.Owner, "Unassigned"),
			board.EmptyAs(task.Branch, "(none)"),
			board.EmptyAs(task.Dependencies, "none"),
			board.EmptyAs(task.BlockingTasks, "none"),
			taskSummary(task.Body),
		)
	}

	if !wrote {
		b.WriteString("- none\n")
	}
}

func taskSummary(body string) string {
	lines := strings.Split(body, "\n")
	var pruned []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Strip heavy structural markdown junk that doesn't add context
		if strings.HasPrefix(trimmed, "### ") ||
			strings.HasPrefix(trimmed, "Owner:") ||
			strings.HasPrefix(trimmed, "Branch:") ||
			strings.HasPrefix(trimmed, "Status:") {
			continue
		}
		// Strip common Markdown checklist brackets [ ] or [x] to save tokens
		trimmed = strings.NewReplacer("[ ] ", "", "[x] ", "", "- ", "").Replace(trimmed)

		if trimmed != "" {
			pruned = append(pruned, trimmed)
		}
		if len(pruned) >= 2 { // Keep a strict max of 2 meaningful context lines per background task
			break
		}
	}

	if len(pruned) == 0 {
		return "no summary"
	}
	return strings.Join(pruned, " | ")
}
