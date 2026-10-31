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

// isCompletedSection checks whether a section heading indicates completed/archived work.
// It strips emojis and symbols to safely handle headings like "## 📦 Archive" or "## Done"
// without falsely matching phrases like "Definition of Done".
func isCompletedSection(section string) bool {
	var sb strings.Builder
	for _, r := range section {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			sb.WriteRune(r)
		}
	}
	clean := strings.ToLower(sb.String())
	return clean == "done" || clean == "archive" || clean == "completed"
}

// parseMetadataLine safely extracts key-value metadata from a task line.
// It tolerates markdown list bullets (- , * , • ) and bold/italic markup (** __)
// around the key and value (e.g. "- **Owner:** Agent 1" or "* Status: In Progress").
func parseMetadataLine(line string) (key, val string, ok bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimLeft(trimmed, "-*• \t")
	trimmed = strings.TrimPrefix(trimmed, "**")
	trimmed = strings.TrimPrefix(trimmed, "__")

	idx := strings.Index(trimmed, ":")
	if idx == -1 {
		return "", "", false
	}

	rawKey := strings.TrimSpace(trimmed[:idx])
	rawKey = strings.TrimRight(rawKey, "*_ \t")

	val = strings.TrimSpace(trimmed[idx+1:])
	// If the colon was inside bold/italic tags (e.g. **Category:** value),
	// the closing tag appears at the start of val.
	val = strings.TrimPrefix(val, "**")
	val = strings.TrimPrefix(val, "__")
	val = strings.Trim(val, "*_ \t")

	return strings.ToLower(rawKey), val, true
}

// normalizeStatus canonicalizes task status strings to standard values ("In Progress",
// "Backlog", "Blocked", "Done"). It normalizes casing, hyphens, and whitespace.
func normalizeStatus(raw string) string {
	clean := strings.TrimSpace(raw)
	lower := strings.ToLower(clean)
	lower = strings.ReplaceAll(lower, "-", " ")
	lower = strings.ReplaceAll(lower, "_", " ")
	lower = strings.Join(strings.Fields(lower), " ")

	switch lower {
	case "in progress":
		return "In Progress"
	case "backlog":
		return "Backlog"
	case "blocked":
		return "Blocked"
	case "done":
		return "Done"
	default:
		return clean
	}
}

// ReadTasks parses a single board file into Tasks. It stops parsing at a
// completed/archive section to save tokens, since those bodies are never needed.
func ReadTasks(path string) ([]Task, error) {
	data, err := fileCache.Read(path)
	if err != nil {
		return nil, err
	}
	return ParseTasks(data), nil
}

// ParseTasks parses task entries from Markdown contents. It lets read-only
// tools validate injected filesystem contents without using the board cache.
func ParseTasks(data string) []Task {

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
			if isCompletedSection(currentSection) {
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

		if k, v, ok := parseMetadataLine(trimmed); ok {
			switch k {
			case "owner":
				current.Owner = v
			case "branch":
				current.Branch = v
			case "status":
				current.Status = normalizeStatus(v)
			case "category":
				current.Category = v
			case "task id":
				current.ID = v
			case "dependencies":
				current.Dependencies = v
			case "blocking tasks":
				current.BlockingTasks = v
			}
		}

		body = append(body, line)
	}

	flush()
	return tasks
}

// EmptyAs returns value, or fallback when value is blank. A small shared helper
// for rendering optional task fields.
func EmptyAs(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
