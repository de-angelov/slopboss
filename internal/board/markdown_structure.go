package board

import "strings"

// markdownFence tracks fenced code so shell comments and headings inside code
// never become board structure. It accepts backtick and tilde fences.
type markdownFence struct {
	marker byte
	length int
}

func (f *markdownFence) contains(line string) bool {
	s := strings.TrimSpace(line)
	if f.marker != 0 {
		n := 0
		for n < len(s) && s[n] == f.marker {
			n++
		}
		if n >= f.length && strings.TrimSpace(s[n:]) == "" {
			f.marker = 0
			f.length = 0
		}
		return true
	}
	if len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return false
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return false
	}
	f.marker = s[0]
	f.length = n
	return true
}

// These conventional task subsections are content even when authors use level
// two headings. Other level-two headings retain their board-section meaning.
func isTaskBodyHeading(line string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## "))) {
	case "objective", "scope", "out of scope", "acceptance criteria", "verification", "result", "results", "progress notes", "evidence", "human ask", "context", "implementation", "notes", "plan", "summary":
		return true
	default:
		return false
	}
}
