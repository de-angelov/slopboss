package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// boardTemplates holds starter content for the root board/config files, keyed by
// filename. TASKS.md is generated separately (tasksTemplate) because its lanes
// depend on the dev-agent count. These are intentionally minimal starters — the
// instruction files are meant to be customized per project.
var boardTemplates = map[string]string{
	"BACKLOG.md": `# BACKLOG

Prioritized backlog. The Team Lead grooms this file and promotes the top task
into a dev-agent lane in TASKS.md.

## Backlog

<!-- Add tasks below as:

### Task title
Owner: (unassigned)
Branch: agent/1/short-slug
Status: Backlog

Describe the task here.
-->
`,

	"ARCHIVE.md": `# ARCHIVE

Completed work history. Normal orchestrator prompts do not load this file.

## Done
`,

	"AGENTS.md": `# AGENTS

Common rules for every agent in this workflow. Customize for your project.

- Work only on your assigned task; keep changes focused and minimal.
- Verify with the project's existing test/build stack before marking work done.
- If blocked by something out of scope, mark the task Blocked with details
  rather than expanding scope.
`,

	"DEV_AGENT.md": `# DEV AGENT

Role-specific instructions for dev agents. Customize for your project.

- Implement the assigned task on its branch and keep the diff tight.
- Prefer the existing test style; assert user-visible behavior over internals.
- Report unrelated failures as Blocked instead of fixing out-of-scope files.
`,

	"TEAM_LEAD_AGENT.md": `# TEAM LEAD AGENT

Role-specific instructions for the Team Lead. Customize for your project.

- Groom and prioritize the backlog. No code implementation during grooming.
- Keep one clear next task ready for each dev-agent lane.
- Prioritize a narrow unblocker task when a lane is Blocked.
`,

	"TECH.md": `# TECH

Describe the product repository's tech stack, commands, and conventions here so
agents have the context they need. Customize for your project.

- Language / framework:
- Install:
- Test:
- Build / typecheck:
`,
}

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
func scaffoldBoardFiles(root string, devAgents int) ([]string, error) {
	files := make(map[string]string, len(boardTemplates)+1)
	for name, content := range boardTemplates {
		files[name] = content
	}
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
