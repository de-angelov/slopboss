package board

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/de-angelov/slopboss/internal/config"
)

// ClearCompletedHumanInput removes a stale human-input bullet only when its
// task is recorded Done in the archive. The board lock covers lookup and edit.
func ClearCompletedHumanInput(id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "\r\n\t ") {
		return false, fmt.Errorf("task ID must be a single token")
	}
	unlock, err := lockBoard()
	if err != nil {
		return false, err
	}
	defer unlock()
	path, source, err := findTaskSource(id)
	if err != nil {
		return false, err
	}
	task := parseAllTasks("## "+sectionAt(source.data, source.start)+"\n"+source.block, path)[id]
	if path != config.ArchiveFile || task.Status != "Done" {
		return false, fmt.Errorf("task %q is not archived as Done", id)
	}
	raw, err := os.ReadFile(config.BacklogFile)
	if err != nil {
		return false, err
	}
	lines := strings.SplitAfter(string(raw), "\n")
	inside := false
	found := -1
	end := -1
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "## ") {
			if found >= 0 && end < 0 {
				end = i
			}
			inside = strings.EqualFold(trim, "## Awaiting Human Input")
			continue
		}
		if !inside {
			continue
		}
		if strings.HasPrefix(trim, "- ") {
			if found >= 0 && end < 0 {
				end = i
			}
			rest := strings.TrimSpace(strings.TrimPrefix(trim, "- "))
			next, _ := utf8.DecodeRuneInString(strings.TrimPrefix(rest, id))
			if strings.HasPrefix(rest, id) && (len(rest) == len(id) || strings.ContainsRune(" \t:—-", next)) {
				if found >= 0 {
					return false, fmt.Errorf("multiple human-input entries for %q", id)
				}
				found = i
				end = -1
			}
		}
	}
	if found < 0 {
		return false, nil
	}
	if end < 0 {
		end = len(lines)
	}
	// Remove only the bullet and its indented continuation lines, preserving any
	// following standalone prose or blank lines.
	stop := found + 1
	for stop < end {
		line := strings.TrimSuffix(lines[stop], "\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			stop++
			continue
		}
		break
	}
	candidate := strings.Join(append(lines[:found], lines[stop:]...), "")
	if err := validateWholeDocument(candidate, "backlog"); err != nil {
		return false, err
	}
	if err := writeAtomic(config.BacklogFile, candidate); err != nil {
		return false, err
	}
	return true, nil
}
