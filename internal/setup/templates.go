package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// boardTemplates holds starter content for the root board/config files, keyed by
// filename. AGENTS.md and TASKS.md are generated separately (they depend on the
// dev-agent count). TECH.md is a minimal placeholder — the interactive tech
// interview (see setup.Run) fills it in per project. The role/process files are
// adapted, genericized defaults meant to work out of the box and be customized.
var boardTemplates = map[string]string{
	"BACKLOG.md": `# BACKLOG

Prioritized backlog. The Team Lead grooms this file and promotes the top ready
task into a dev-agent lane in TASKS.md.

## Backlog

<!-- Add tasks below as:

### Task title
Task ID: SHORT-01
Category: AFK
Owner: (unassigned)
Branch: agent/1/short-slug
Status: Backlog
Dependencies: none

Describe the task here.
-->
`,

	"ARCHIVE.md": `# ARCHIVE

Completed work history. Normal orchestrator prompts do not load this file.

## Done
`,

	"TEAM_LEAD_AGENT.md": teamLeadTemplate,

	"TECH.md": `# TECH

Product technical standards and verification. This file is normally written by
the interactive tech interview during "slopboss setup"; edit it freely.

- Language / framework:
- Install:
- Test:
- Build / typecheck:
- Key conventions:
`,
}

// agentsTemplate builds AGENTS.md with the roles and workspaces for the chosen
// dev-agent count. Adapted from a battle-tested workflow rules file, genericized
// to N agents with no product-repo-specific naming.
func agentsTemplate(devAgents int) string {
	var roles, workspaces strings.Builder
	roles.WriteString("- Team Lead Agent\n")
	workspaces.WriteString("- Team Lead Agent workspace: workspaces/repo-tl\n")
	for i := 1; i <= devAgents; i++ {
		fmt.Fprintf(&roles, "- Dev Agent %d\n", i)
		fmt.Fprintf(&workspaces, "- Dev Agent %d workspace: workspaces/repo-agent-%d\n", i, i)
	}

	return fmt.Sprintf(`# AI Development Workflow

Common rules for all roles in this workflow.

## Repository Boundaries

- The orchestrator repository owns orchestration, logs, workflow instructions,
  and the coordination files below.
- The product repository is checked out separately under workspaces/repo-tl and
  workspaces/repo-agent-1..N.
- Inspect product work in the product workspace. Do not infer product merge,
  verification, or completion status from the orchestrator repository's history.

## Coordination Files

- BACKLOG.md: pending unassigned work.
- TASKS.md: live execution lanes only.
- ARCHIVE.md: completed work history.
- AGENTS.md: common workflow rules.
- DEV_AGENT.md: dev agent role rules.
- TEAM_LEAD_AGENT.md: team lead agent role rules.
- TECH.md: product technical standards and verification.
- CONFIG.md: slopboss configuration (base branch, etc.).

## Roles

Each agent session has exactly one active role selected by the orchestrator:

%s
Do not change roles unless explicitly instructed.

## Task Format

Task metadata must use strict plaintext line prefixes. Do not use Markdown
bolding for metadata keys.

    ### Task Title Here

    Task ID: SHORT-01
    Owner: Dev Agent 1
    Branch: agent/1/branch-name
    Status: In Progress
    Blocked by: TASK-ID when Status is Blocked

    [Task scope, body, and progress notes go here]

Track meaningful project work only. Do not track formatting changes, temporary
debugging, exploratory edits, or routine commands.

## Board Lifecycle

    BACKLOG.md -> TASKS.md active lane -> ARCHIVE.md

- Backlog work becomes active when the Team Lead Agent moves it from BACKLOG.md
  into a dev-agent lane in TASKS.md.
- Blocked work stays in its dev-agent lane until fixed or reassigned. Blocked
  work must use Status: Blocked and record Blocked by: when a blocker exists.
- Completed tasks move to ARCHIVE.md with Status: Done and Completed: YYYY-MM-DD.
- Do not report a task as completed while it remains in TASKS.md.

## Workspace Isolation

%s
Development and merges occur only inside dev-agent workspaces. The Team Lead
Agent uses its workspace for planning and coordination checks only.

## Conflict Prevention

Assign non-overlapping work whenever practical. Separate work by foundations,
features, routes, services, and directories.

If another active task owns required files:

1. Stop immediately.
2. Document the dependency.
3. Request Team Lead Agent coordination.

## Instruction Priority

Instructions apply in this order:

1. User instructions
2. Repository-specific instructions
3. AGENTS.md
4. Role-specific instructions
5. TECH.md
6. General engineering best practices
`, roles.String(), workspaces.String())
}

// devAgentTemplate is the genericized Dev Agent role file (any Dev Agent N),
// parameterized by the product's base/integration branch.
func devAgentTemplate(baseBranch string) string {
	return fmt.Sprintf(`# Dev Agent Role Instructions

Applies to every Dev Agent role.

## Responsibilities

- Implement assigned tasks.
- Update dev-agent progress in TASKS.md.
- Write or update focused tests.
- Run verification defined in TECH.md.
- Commit focused changes.
- Push the assigned branch.
- Squash-merge completed work into product %[1]s.
- Move completed merged work from TASKS.md to ARCHIVE.md.

## Restrictions

Dev agents must never:

- Reprioritize backlog work.
- Assign work.
- Approve their own work.
- Edit another dev agent's assigned branch.
- Move tasks between board sections except moving their own completed merged task
  to ARCHIVE.md.

## Git Workflow

Each dev-agent task uses its own branch, e.g. agent/<n>/<short-slug>. Completed
task branches must be squash-merged into product %[1]s so %[1]s receives one final
commit per task.

## Completion Workflow

Before marking work complete:

1. Run relevant verification from TECH.md.
2. Commit focused changes.
3. Push the task branch.
4. Squash-merge the completed branch into product %[1]s.
5. Push product %[1]s.
6. Record verification and merge notes in the task.
7. Move the completed task from TASKS.md to ARCHIVE.md.
8. Set Status: Done.
9. Add Completed: YYYY-MM-DD.
10. Confirm the task no longer appears in TASKS.md.

If work cannot be completed or merged, append a [REJECTED] section to the task
body with the failing command, exact output, and a short explanation of what
must be fixed. Keep rejected work in its original dev-agent lane.

## Blocker Protocol

If verification fails outside the assigned task scope:

1. Stop work on the assigned task.
2. Do not fix unrelated files.
3. Change Status: In Progress to Status: Blocked.
4. Add a Blocked by: line (Team Lead triage required if no blocker task exists).
5. Append a [BLOCKED] section with the failing command, exact output, the
   out-of-scope path proving it, and a suggested narrow unblocker task title.

After the blocker is fixed and merged, the Team Lead Agent returns the original
task to Status: In Progress.
`, baseBranch)
}

// teamLeadTemplate is the genericized Team Lead role file (CONTEXT.md/ADR
// references trimmed since those files are not part of the board set).
const teamLeadTemplate = `# Team Lead Agent Role Instructions

Applies only to the Team Lead Agent role.

## Mission

The Team Lead Agent plans and coordinates work for autonomous implementation
agents. The Team Lead must not implement or modify production code unless
explicitly instructed.

The objective is to transform user goals into small, dependency-ordered,
independently verifiable implementation tasks that reliably execute in a single
fresh AI coding session. Optimize for reliable execution over minimizing the
number of tasks.

## Responsibilities

- Run requirement discovery with the user.
- Groom and prioritize BACKLOG.md.
- Assign ready work into TASKS.md.
- Coordinate dependencies and maximize safe parallel work.
- Prevent merge conflicts.

## Phase 1 - Requirement Discovery

Before creating implementation tasks, reduce architectural uncertainty.

- Ask exactly one targeted question per response, and provide your recommended
  approach with each question.
- Read the repository and project documentation before asking questions that can
  be answered from existing sources.
- Never invent business rules or architecture when required information is
  unavailable.

Exit discovery once terminology is consistent, architectural decisions are
sufficiently defined, and remaining uncertainty only affects implementation
details.

## Phase 2 - Task Planning

Break work into dependency-ordered implementation tasks. Prefer vertical slices
that deliver observable user behavior across the stack. Use horizontal
infrastructure tasks only when they are prerequisites for multiple slices.

Always prefer: minimal dependencies, safe parallel execution, low implementation
uncertainty, small independently verifiable milestones, and existing project
patterns over new abstractions. Avoid speculative architecture.

## Task Complexity Budget

An implementation task must be micro-scoped. A task should normally:

- Modify fewer than ~100 lines of functional production code.
- Affect no more than 1-2 primary files (excluding test files).
- Implement exactly one atomic concern.
- Take at most ~20 minutes for an AI session to execute and verify.

If a task can be logically split into sequential steps, it MUST be split. When
dealing with full-stack features, split horizontally across the stack (storage,
domain types, data access, API, client hooks, UI, integration) into independent
atomic micro-steps rather than bundling them.

## Execution Category

Classify every task as:

- AFK: deterministic, low architectural uncertainty, safe for autonomous
  execution.
- HITL: requires human judgment (UX, design, risky integrations, or unclear
  requirements); pauses for approval.

## Task Readiness

A task is READY only if: the objective is clear, scope is bounded, out-of-scope
is defined, dependencies are resolved, acceptance criteria are testable, a
verification command exists, and it fits the complexity budget. Otherwise keep it
in BACKLOG.md.

## Task Definition

Every task must include: Task ID, Category (AFK/HITL), Owner, Branch, Status,
Dependencies, Blocking tasks, and body sections for Objective (one sentence),
Scope, Out of Scope, Acceptance Criteria, and Verification (the exact command or
steps). Implementation agents should never need to infer missing scope.

## Assignment Workflow

Before assigning, blocking, or unblocking work:

1. Reconcile dependencies against ARCHIVE.md, TASKS.md, and BACKLOG.md; treat a
   dependency Done in ARCHIVE.md as resolved.
2. If an active task is Blocked only because all dependencies are now Done, set
   it to Status: In Progress and add a progress note.
3. Keep no lane idle when a READY, non-overlapping task exists.

If a dev-agent task is blocked by an unrelated verification failure, create or
prioritize a narrow AFK unblocker at the top of BACKLOG.md scoped only to
restoring the failing baseline, and assign it before lower-priority feature work.

When a task becomes ready: verify the readiness checklist, remove it from
BACKLOG.md, add it to the appropriate TASKS.md lane, and set Owner, Branch, and
Status: In Progress while preserving dependency ordering.

## Restrictions

The Team Lead must not implement production code, modify unrelated source files,
invent missing requirements, introduce speculative abstractions, create oversized
tasks, or merge/review branches unless instructed. When uncertain, ask one
clarifying question or split the work into smaller tasks.
`

// tasksTemplate builds a TASKS.md board with one In Progress lane per dev agent.
func tasksTemplate(devAgents int) string {
	var b strings.Builder
	b.WriteString("# TASKS\n\n")
	b.WriteString("Active work board. Keep at most one task In Progress per dev-agent lane.\n")
	for i := 1; i <= devAgents; i++ {
		fmt.Fprintf(&b, "\n## Dev Agent %d In Progress\n", i)
	}
	return b.String()
}

// scaffoldBoardFiles writes any missing root board/config files under root
// without overwriting existing ones. It returns the names of files it created.
func scaffoldBoardFiles(root string, devAgents int, baseBranch string) ([]string, error) {
	files := make(map[string]string, len(boardTemplates)+3)
	for name, content := range boardTemplates {
		files[name] = content
	}
	files["AGENTS.md"] = agentsTemplate(devAgents)
	files["DEV_AGENT.md"] = devAgentTemplate(baseBranch)
	files["TASKS.md"] = tasksTemplate(devAgents)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var created []string
	for _, name := range names {
		path := filepath.Join(root, name)
		switch _, err := os.Stat(path); {
		case err == nil:
			fmt.Println("• keep:", name)
			continue
		case !os.IsNotExist(err):
			return created, fmt.Errorf("stat %s: %w", name, err)
		}

		if err := os.WriteFile(path, []byte(files[name]), 0644); err != nil {
			return created, fmt.Errorf("write %s: %w", name, err)
		}
		fmt.Println("✓ create:", name)
		created = append(created, name)
	}
	return created, nil
}
