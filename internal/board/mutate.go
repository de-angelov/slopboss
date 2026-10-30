package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
)

// Update is the structural portion of a task an agent may change through the
// board command. Empty fields mean "leave unchanged"; ClearBlockedBy explicitly
// removes a resolved blocker.
type Update struct {
	Owner          string
	Branch         string
	Status         string
	Category       string
	Dependencies   string
	BlockedBy      string
	ClearBlockedBy bool
	File           string
	Section        string
}

// CreateBacklogTask adds a fully specified, unassigned task to Backlog.
func CreateBacklogTask(task Task) (Task, error) {
	task.ID = strings.TrimSpace(task.ID)
	task.Title = strings.TrimSpace(task.Title)
	if task.ID == "" || task.Title == "" || strings.TrimSpace(task.Body) == "" {
		return Task{}, fmt.Errorf("task ID, title, and body are required")
	}
	if _, _, err := findTaskSource(task.ID); err == nil {
		return Task{}, fmt.Errorf("task %q already exists", task.ID)
	}
	if !strings.EqualFold(task.Category, "AFK") && !strings.EqualFold(task.Category, "HITL") {
		return Task{}, fmt.Errorf("category must be AFK or HITL")
	}
	task.Section, task.Owner, task.Status = "Backlog", "Unassigned", "Backlog"
	if strings.TrimSpace(task.Branch) == "" {
		task.Branch = "none"
	}
	if strings.TrimSpace(task.Dependencies) == "" {
		task.Dependencies = "none"
	}
	if err := validateTaskBody(task.Body); err != nil {
		return Task{}, err
	}
	if err := validateDependencies(task.ID, task.Dependencies); err != nil {
		return Task{}, err
	}
	block := fmt.Sprintf("### %s\n\nTask ID: %s\nCategory: %s\nOwner: Unassigned\nBranch: %s\nStatus: Backlog\nDependencies: %s\nBlocking tasks: none\n\n%s\n", task.Title, task.ID, strings.ToUpper(task.Category), task.Branch, task.Dependencies, strings.TrimSpace(task.Body))
	data, err := os.ReadFile(config.BacklogFile)
	if err != nil {
		return Task{}, err
	}
	content := string(data)
	if !strings.Contains(content, "## Backlog") {
		return Task{}, fmt.Errorf("BACKLOG.md has no ## Backlog section")
	}
	if err := writeAtomic(config.BacklogFile, strings.TrimRight(content, "\n")+"\n\n"+block); err != nil {
		return Task{}, err
	}
	return Get(task.ID)
}

func validateTaskBody(body string) error {
	required := []string{"Objective:", "Scope:", "Out of Scope:", "Acceptance Criteria:", "Verification:"}
	for _, section := range required {
		if !strings.Contains(body, section) {
			return fmt.Errorf("task body is missing required section %q", section)
		}
	}
	return nil
}

func validateDependencies(taskID, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("dependency list contains an empty task ID")
		}
		if id == taskID {
			return fmt.Errorf("task %q cannot depend on itself", taskID)
		}
		if _, _, err := findTaskSource(id); err != nil {
			return fmt.Errorf("dependency %q does not exist on the board", id)
		}
	}
	return nil
}

// Get returns the task with id from any board file. It deliberately reads the
// archive too: normal scheduler reads skip historic archive sections for speed.
func Get(id string) (Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, fmt.Errorf("task ID is required")
	}
	for _, path := range boardPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			return Task{}, err
		}
		if found, ok := parseAllTasks(string(data), path)[id]; ok {
			return found, nil
		}
	}
	return Task{}, fmt.Errorf("task %q not found", id)
}

// Set applies a validated structural update. Moving a task requires both File
// and Section so an accidental status edit can never relocate work.
func Set(id string, update Update) (Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, fmt.Errorf("task ID is required")
	}
	if err := validateUpdate(update); err != nil {
		return Task{}, err
	}

	sourcePath, source, err := findTaskSource(id)
	if err != nil {
		return Task{}, err
	}
	targetPath := sourcePath
	if update.File != "" {
		targetPath, err = boardPath(update.File)
		if err != nil {
			return Task{}, err
		}
	}
	if update.Section != "" && update.File == "" {
		return Task{}, fmt.Errorf("--section requires --file so the destination is explicit")
	}
	if lifecycleChange(update) {
		current, err := Get(id)
		if err != nil {
			return Task{}, err
		}
		candidate := applyUpdateToTask(current, update)
		if _, err := ValidateLifecycle(candidate, boardFileName(targetPath)); err != nil {
			return Task{}, err
		}
	}

	block := applyMetadata(source.block, update)
	if update.Section != "" {
		block = moveTask(source.data, source.start, source.end, block, targetPath, update.Section)
		if sourcePath == targetPath {
			if err := writeAtomic(sourcePath, block); err != nil {
				return Task{}, err
			}
		} else {
			targetData, err := os.ReadFile(targetPath)
			if err != nil {
				return Task{}, err
			}
			newTarget := moveTask(source.data, source.start, source.end, block, targetPath, update.Section)
			// Commit destination first so interruption cannot lose the task. If
			// removing the source fails, restore the original destination.
			if err := writeAtomic(targetPath, newTarget); err != nil {
				return Task{}, err
			}
			if err := writeAtomic(sourcePath, source.data[:source.start]+source.data[source.end:]); err != nil {
				_ = writeAtomic(targetPath, string(targetData))
				return Task{}, err
			}
		}
	} else {
		if err := writeAtomic(sourcePath, source.data[:source.start]+block+source.data[source.end:]); err != nil {
			return Task{}, err
		}
	}
	return Get(id)
}

func lifecycleChange(u Update) bool {
	return u.Owner != "" || u.Status != "" || u.File != "" || u.Section != ""
}

func applyUpdateToTask(task Task, u Update) Task {
	if u.Owner != "" {
		task.Owner = u.Owner
	}
	if u.Status != "" {
		task.Status = normalizeStatus(u.Status)
	}
	if u.Section != "" {
		task.Section = u.Section
	}
	return task
}

type taskSource struct {
	data       string
	block      string
	start, end int
}

func findTaskSource(id string) (string, taskSource, error) {
	for _, path := range boardPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", taskSource{}, err
		}
		for _, block := range taskBlocks(string(data)) {
			if metadataValue(block.text, "task id") == id {
				return path, taskSource{data: string(data), block: block.text, start: block.start, end: block.end}, nil
			}
		}
	}
	return "", taskSource{}, fmt.Errorf("task %q not found", id)
}

func validateUpdate(u Update) error {
	if (u.File == "") != (u.Section == "") {
		return fmt.Errorf("--file and --section must be supplied together when moving a task")
	}
	if u.File != "" {
		if _, err := boardPath(u.File); err != nil {
			return err
		}
	}
	if strings.HasPrefix(strings.TrimSpace(u.Section), "Team Lead") {
		return fmt.Errorf("Team Lead coordination tasks stay in BACKLOG.md with Status: Backlog; do not create a Team Lead lane")
	}
	if u.Status != "" {
		switch normalizeStatus(u.Status) {
		case "Backlog", "In Progress", "Blocked", "Done":
		default:
			return fmt.Errorf("invalid status %q (use Backlog, In Progress, Blocked, or Done)", u.Status)
		}
	}
	if u.Category != "" && !strings.EqualFold(u.Category, "AFK") && !strings.EqualFold(u.Category, "HITL") {
		return fmt.Errorf("invalid category %q (use AFK or HITL)", u.Category)
	}
	return nil
}

func boardPaths() []string { return []string{config.BacklogFile, config.TasksFile, config.ArchiveFile} }

func boardPath(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "backlog":
		return config.BacklogFile, nil
	case "tasks":
		return config.TasksFile, nil
	case "archive":
		return config.ArchiveFile, nil
	default:
		return "", fmt.Errorf("invalid board file %q (use backlog, tasks, or archive)", name)
	}
}

type blockSpan struct {
	start, end int
	text       string
}

func taskBlocks(data string) []blockSpan {
	var blocks []blockSpan
	start := -1
	offset := 0
	for _, line := range strings.SplitAfter(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") && start >= 0 {
			blocks = append(blocks, blockSpan{start: start, end: offset, text: data[start:offset]})
			start = -1
		}
		if strings.HasPrefix(trimmed, "### ") {
			if start >= 0 {
				blocks = append(blocks, blockSpan{start: start, end: offset, text: data[start:offset]})
			}
			start = offset
		}
		offset += len(line)
	}
	if start >= 0 {
		blocks = append(blocks, blockSpan{start: start, end: len(data), text: data[start:]})
	}
	return blocks
}

func metadataValue(block, key string) string {
	for _, line := range strings.Split(block, "\n") {
		if k, v, ok := parseMetadataLine(line); ok && k == key {
			return v
		}
	}
	return ""
}

func applyMetadata(block string, u Update) string {
	values := map[string]string{}
	if u.Owner != "" {
		values["owner"] = u.Owner
	}
	if u.Branch != "" {
		values["branch"] = u.Branch
	}
	if u.Status != "" {
		values["status"] = normalizeStatus(u.Status)
	}
	if u.Category != "" {
		values["category"] = strings.ToUpper(u.Category)
	}
	if u.Dependencies != "" {
		values["dependencies"] = u.Dependencies
	}
	if u.BlockedBy != "" {
		values["blocked by"] = u.BlockedBy
	}

	lines := strings.Split(block, "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		if key, _, ok := parseMetadataLine(line); ok {
			if value, change := values[key]; change {
				lines[i] = canonicalMetadataKey(key) + ": " + value
				seen[key] = true
			}
			if key == "blocked by" && u.ClearBlockedBy {
				lines[i] = ""
			}
		}
	}
	var add []string
	for _, key := range []string{"owner", "branch", "status", "category", "dependencies", "blocked by"} {
		if value, ok := values[key]; ok && !seen[key] {
			add = append(add, canonicalMetadataKey(key)+": "+value)
		}
	}
	if len(add) > 0 {
		lines = append(lines[:1], append(add, lines[1:]...)...)
	}
	return strings.Join(lines, "\n")
}

func canonicalMetadataKey(key string) string {
	switch key {
	case "blocked by":
		return "Blocked by"
	case "task id":
		return "Task ID"
	default:
		return strings.ToUpper(key[:1]) + key[1:]
	}
}

func moveTask(source string, start, end int, block, targetPath, section string) string {
	remaining := source[:start] + source[end:]
	if targetPath == "" {
		return remaining
	}
	data, _ := os.ReadFile(targetPath)
	target := string(data)
	if target == source {
		target = remaining
	}
	heading := "## " + section
	if idx := strings.Index(target, heading+"\n"); idx >= 0 {
		insert := idx + len(heading) + 1
		return target[:insert] + "\n" + strings.TrimSpace(block) + "\n" + target[insert:]
	}
	return strings.TrimRight(target, "\n") + "\n\n" + heading + "\n\n" + strings.TrimSpace(block) + "\n"
}

func writeAtomic(path, data string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".slopboss-board-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func parseAllTasks(data, path string) map[string]Task {
	section := ""
	result := map[string]Task{}
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		}
	}
	for _, span := range taskBlocks(data) {
		t := Task{Title: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.Split(span.text, "\n")[0]), "### "))}
		// Determine the section from the portion immediately before this block.
		prefix := data[:span.start]
		for _, line := range strings.Split(prefix, "\n") {
			if s := strings.TrimSpace(line); strings.HasPrefix(s, "## ") && !strings.HasPrefix(s, "### ") {
				t.Section = strings.TrimSpace(strings.TrimPrefix(s, "## "))
			}
		}
		for _, line := range strings.Split(span.text, "\n") {
			if k, v, ok := parseMetadataLine(line); ok {
				switch k {
				case "task id":
					t.ID = v
				case "owner":
					t.Owner = v
				case "branch":
					t.Branch = v
				case "status":
					t.Status = normalizeStatus(v)
				case "category":
					t.Category = v
				case "dependencies":
					t.Dependencies = v
				case "blocking tasks", "blocked by":
					t.BlockingTasks = v
				}
			}
		}
		t.Body = strings.TrimSpace(span.text)
		t.Key = taskFingerprint(t)
		if t.ID != "" {
			result[t.ID] = t
		}
	}
	_ = section
	_ = path
	return result
}
