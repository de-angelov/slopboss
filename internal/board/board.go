// Package board is slopboss's model of the Markdown task board. It parses
// BACKLOG.md / TASKS.md / ARCHIVE.md into Tasks, answers scheduling questions
// about them (what's assignable, what's blocked, what's complete), and folds in
// work already merged to origin/main. It is the shared domain vocabulary the
// orchestrator, prompt builder, experiment runner, and TUI all speak.
package board

import (
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
)

// Task is one parsed board entry: a titled block with its metadata fields and
// raw body. Key is a fingerprint used to detect board edits across polls.
type Task struct {
	Section       string
	Title         string
	ID            string
	Owner         string
	Branch        string
	Status        string
	Category      string
	Dependencies  string
	BlockingTasks string
	Body          string
	Key           string
}

// ReadBoardTasks parses the active board files (backlog + lanes + archive) into
// a single task slice.
func ReadBoardTasks() ([]Task, error) {
	var all []Task

	for _, path := range []string{config.BacklogFile, config.TasksFile, config.ArchiveFile} {
		tasks, err := ReadTasks(path)
		if err != nil {
			return nil, err
		}
		all = append(all, tasks...)
	}

	return all, nil
}

func taskFingerprint(task Task) string {
	return strings.Join([]string{
		task.Section,
		task.Title,
		task.Owner,
		task.Branch,
		task.Status,
	}, "\x00")
}

// ReadTasks parses a single board file into Tasks. It stops parsing at a
// completed/archive section to save tokens, since those bodies are never needed.
func ReadTasks(path string) ([]Task, error) {
	data, err := fileCache.Read(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(data, "\n")

	var tasks []Task
	var current *Task
	var body []string
	currentSection := ""

	flush := func() {
		if current == nil {
			return
		}

		current.Body = strings.TrimSpace(strings.Join(body, "\n"))
		current.Key = taskFingerprint(*current)
		tasks = append(tasks, *current)

		current = nil
		body = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
			flush()
			currentSection = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))

			// TOKEN OPTIMIZATION: Stop parsing if we hit historical/completed logs
			if currentSection == "Done" || currentSection == "Archive" || currentSection == "Completed" {
				currentSection = "SKIP"
			}
			continue
		}

		// Fast-forward past skipped sections entirely
		if currentSection == "SKIP" {
			continue
		}

		if strings.HasPrefix(trimmed, "### ") {
			flush()

			current = &Task{
				Section: currentSection,
				Title:   strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")),
			}

			body = append(body, line)
			continue
		}

		if current == nil {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "Owner:"):
			current.Owner = strings.TrimSpace(strings.TrimPrefix(trimmed, "Owner:"))
		case strings.HasPrefix(trimmed, "Branch:"):
			current.Branch = strings.TrimSpace(strings.TrimPrefix(trimmed, "Branch:"))
		case strings.HasPrefix(trimmed, "Status:"):
			current.Status = strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:"))
		case strings.HasPrefix(trimmed, "Category:"):
			current.Category = strings.TrimSpace(strings.TrimPrefix(trimmed, "Category:"))
		case strings.HasPrefix(trimmed, "Task ID:"):
			current.ID = strings.TrimSpace(strings.TrimPrefix(trimmed, "Task ID:"))
		case strings.HasPrefix(trimmed, "Dependencies:"):
			current.Dependencies = strings.TrimSpace(strings.TrimPrefix(trimmed, "Dependencies:"))
		case strings.HasPrefix(trimmed, "Blocking tasks:"):
			current.BlockingTasks = strings.TrimSpace(strings.TrimPrefix(trimmed, "Blocking tasks:"))
		}

		body = append(body, line)
	}

	flush()
	return tasks, nil
}

// EmptyAs returns value, or fallback when value is blank. A small shared helper
// for rendering optional task fields.
func EmptyAs(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
