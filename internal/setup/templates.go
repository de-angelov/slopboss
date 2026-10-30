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

<one short paragraph: what this product is>. Dev agents rely on this file;
normally written by the setup tech quiz — edit it freely.

## Technology Stack

- Language:
- Framework:
- Package manager / runtime:

## Commands

- Install:
- Test (full suite, lean reporter):
- Test (targeted single test):
- Build / typecheck:
- Lint / format:

## Tooling & Token Efficiency

- Lean test reporter flags:
- Targeted test syntax:

## Conventions

- Directory layout:
- Testing:
- Definition of done:
- Avoid:
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

- Pending assignable backlog tasks must use Status: Backlog. "Ready" is a
  readiness concept, not a valid backlog Status value.
- Backlog work becomes active when the Team Lead Agent moves it from BACKLOG.md
  into a dev-agent lane in TASKS.md.
- A dev-agent lane may contain at most one In Progress task. Blocked or rejected
  tasks in the lane do not count as active work and do not consume capacity; a
  lane with no In Progress task is available for new work.
- Verification failures outside the assigned task do not make that task Blocked.
  Note the failing command and path in the task, then continue. Blocked is
  reserved for genuine human input; record Blocked by: when a blocker exists.
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
- Run targeted test files during iteration and before completion. The targeted
  tests and build are the merge gate; the full suite is informational.
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
commit per task. The squash commit subject must begin with the task ID, for example
TASK-123: add schema validation; Slopboss uses that ID to recognize merged work
even if an agent exits before it updates ARCHIVE.md.

## Completion Workflow

Before marking work complete:

1. Run the targeted verification from TECH.md for this task.
2. Commit focused changes.
3. Push the task branch.
4. Squash-merge the completed branch into product %[1]s, using a commit subject
   that begins with the task ID (for example TASK-123: concise summary).
5. Push product %[1]s. If push is rejected because origin moved ahead, do not abort or block:
   a. Run: git fetch origin %[1]s && git rebase origin/%[1]s
   b. On conflict: list files with 'git diff --name-only --diff-filter=U' and resolve using the zdiff3 base context.
   c. Verify zero leftover markers: git diff --check (must exit 0).
   d. Complete rebase: git add <resolved-files> && git rebase --continue
   e. Re-run targeted verification, squash-merge, and push product %[1]s.
6. Fetch product %[1]s and verify the recorded squash commit is reachable from
   origin/%[1]s with: git merge-base --is-ancestor <squash-commit> origin/%[1]s.
7. Record verification and merge notes in the task, including the squash commit.
8. Move the completed task from TASKS.md to ARCHIVE.md.
9. Set Status: Done.
10. Add Completed: YYYY-MM-DD.
11. Confirm the task no longer appears in TASKS.md.

## Verification Protocol

1. Your targeted tests and build are the merge gate. Fix failures caused by your
   change before completing.
2. If the full suite fails in an unrelated area, do not fix unrelated files and
   do not mark the task Blocked. Note the failing command and path in the task's
   progress notes, then finish your task.
3. Blocked tasks may remain in their lane; they do not consume lane capacity.
   Handle genuine human dependencies below.

## Human Blocker Protocol

If the assigned task genuinely requires human input (credentials, access, or a
decision) and cannot proceed:

1. Stop work on the assigned task.
2. Set Status: Blocked, add Blocked by:, and state the specific ask.
3. Keep the task in its lane; blocked work does not consume capacity.

After the human dependency is resolved, the Team Lead Agent sets the task back
to Status: In Progress.
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

## Completion Follow-Up

Every Team Lead session begins by reviewing Dev Agent tasks that are In Progress,
Blocked, or whose agent session has just ended. For each one, record exactly one
next state before assigning new work:

1. **Merged** — verify the squash commit is on the product base branch, then
   archive the task as Done.
2. **Ready to merge** — record the focused verification result and direct the
   owning agent to rebase, verify, squash-merge, and archive it.
3. **Blocked** — record the exact failing command, the owning task or human
   dependency, and the smallest dependency-ordered unblocker.
4. **Requeued** — correct scope, ownership, and dependencies when the task was
   assigned incorrectly or its work is incomplete.

Never treat a completed agent session as a completed task: pushed, merged, and
archived are separate facts. Do not start unrelated work until every recently
ended session has one of these next states.

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

READY is a readiness concept, not a metadata Status. A ready unassigned task in
BACKLOG.md must still use Status: Backlog so the scheduler can assign it.

## Task Definition

Every task must include: Task ID, Category (AFK/HITL), Owner, Branch, Status,
Dependencies, Blocking tasks, and body sections for Objective (one sentence),
Scope, Out of Scope, Acceptance Criteria, and Verification (the exact command or
steps). Implementation agents should never need to infer missing scope.
In Scope, provide the concrete target file paths. In Verification, give the
targeted test command first, and list the full-suite command only as a diagnostic.

Use Status: Backlog for unassigned tasks in BACKLOG.md. Do not write
Status: Ready.

## Assignment Workflow

Before assigning, blocking, or unblocking work:

1. Reconcile dependencies against ARCHIVE.md, TASKS.md, and BACKLOG.md; treat a
   dependency Done in ARCHIVE.md as resolved.
2. Treat a dev-agent lane as occupied only by its In Progress task. Blocked or
   rejected tasks do not consume lane capacity; assign ready work into any lane
   that has no In Progress task.
3. If an active task is Blocked only because all dependencies are now Done, set
   it to Status: In Progress and add a progress note.
4. Keep no lane idle when a READY, non-overlapping task exists.

Verification failures outside a dev task do not block that task. The dev agent
notes the failure and continues; do not create a blocked lane for it.

When a task becomes ready: verify the readiness checklist, remove it from
BACKLOG.md, add it to the appropriate TASKS.md lane, and set Owner, Branch, and
Status: In Progress while preserving dependency ordering.

## Restrictions

The Team Lead must not implement production code, modify unrelated source files,
invent missing requirements, introduce speculative abstractions, or create
oversized tasks. It may inspect, rebase, squash-merge, and archive an assigned
task branch when performing the Completion Follow-Up protocol. When uncertain,
ask one clarifying question or split the work into smaller tasks.
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
