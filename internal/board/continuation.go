package board

import (
	"fmt"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
)

// RequestContinuation records a handoff against one observed terminal lease.
// A changed role or branch invalidates the request before it is written.
func RequestContinuation(id, role, branch, leaseID, reason string) (Task, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || strings.ContainsAny(reason, "\r\n") {
		return Task{}, fmt.Errorf("continuation requires a single-line reason")
	}
	if len(leaseID) != 64 || strings.Trim(leaseID, "0123456789abcdef") != "" {
		return Task{}, fmt.Errorf("invalid terminal lease ID")
	}
	unlock, err := lockBoard()
	if err != nil {
		return Task{}, err
	}
	defer unlock()
	path, source, err := findTaskSource(id)
	if err != nil {
		return Task{}, err
	}
	task := parseAllTasks("## "+sectionAt(source.data, source.start)+"\n"+source.block, path)[id]
	if task.Owner != role || task.Branch != branch || !strings.EqualFold(task.Status, "In Progress") || path != config.TasksFile {
		return Task{}, fmt.Errorf("task %q is no longer assigned to %s on the same branch", id, role)
	}
	values := map[string]string{"continue after": leaseID, "continuation instructions": reason}
	lines := strings.Split(source.block, "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		if k, _, ok := parseMetadataLine(line); ok {
			if v, exists := values[k]; exists {
				lines[i] = canonicalMetadataKey(k) + ": " + v
				seen[k] = true
			}
		}
	}
	var add []string
	for _, k := range []string{"continue after", "continuation instructions"} {
		if !seen[k] {
			add = append(add, canonicalMetadataKey(k)+": "+values[k])
		}
	}
	if len(add) > 0 {
		lines = append(lines[:1], append(add, lines[1:]...)...)
	}
	block := strings.Join(lines, "\n")
	candidate := source.data[:source.start] + block + source.data[source.end:]
	if err := validateWholeDocument(candidate, boardFileName(path)); err != nil {
		return Task{}, err
	}
	if err := writeAtomic(path, candidate); err != nil {
		return Task{}, err
	}
	return parseAllTasks("## "+task.Section+"\n"+block, path)[id], nil
}
