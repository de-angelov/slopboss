package board

import (
	"regexp"
	"strings"
)

var (
	verificationSectionRe = regexp.MustCompile(`(?i)(?:^|\n)##+\s*Verification\s*\n([^#]+)`)
	codeFenceRe           = regexp.MustCompile("(?s)```(?:sh|bash)?\\s*\\n?(.*?)\\n?```")
	fullSuiteDiagnosticRe = regexp.MustCompile(`(?i)\s*(?:Full-suite diagnostic|Diagnostic full suite|Diagnostic suite|Full-suite|Diagnostic):.*$`)
)

// ExtractVerification parses the targeted verification command from a task body.
func ExtractVerification(body string) string {
	if match := verificationSectionRe.FindStringSubmatch(body); len(match) > 1 {
		content := strings.TrimSpace(match[1])
		if fence := codeFenceRe.FindStringSubmatch(content); len(fence) > 1 {
			cmd := strings.TrimSpace(fence[1])
			return cleanVerificationCommand(cmd)
		}
		lines := strings.Split(content, "\n")
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t != "" && !strings.HasPrefix(t, ">") {
				return cleanVerificationCommand(t)
			}
		}
	}

	// Line-by-line search for inline "Verification:"
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "verification:") {
			val := strings.TrimSpace(trimmed[len("verification:"):])
			// If subsequent lines exist and don't start with a new field (e.g. "Field:"), gather them
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" || strings.HasPrefix(next, "#") || strings.Contains(next, ":") {
					break
				}
				val += " " + next
			}
			return cleanVerificationCommand(val)
		}
	}

	return ""
}

func cleanVerificationCommand(raw string) string {
	cleaned := fullSuiteDiagnosticRe.ReplaceAllString(raw, "")
	cleaned = strings.TrimSpace(cleaned)

	lines := strings.Split(cleaned, "\n")
	var parts []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" {
			parts = append(parts, t)
		}
	}
	res := strings.Join(parts, " ")

	if strings.HasSuffix(res, ".") && !strings.HasSuffix(res, "..") {
		res = strings.TrimSuffix(res, ".")
	}

	return strings.TrimSpace(res)
}
