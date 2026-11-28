package board

import (
	"fmt"
	"regexp"
	"strings"
)

var verificationHeading = regexp.MustCompile(`(?i)^#{2,}\s+verification\s*$`)
var anotherHeading = regexp.MustCompile(`^#{1,}\s+`)
var proseAfterCommand = regexp.MustCompile(`(?:\.\s+[A-Z][a-zA-Z]+\b|(?i:\b(?:full-suite diagnostic|diagnostic full suite|diagnostic suite|full-suite|diagnostic):))`)

// ParseVerification preserves fenced shell scripts and rejects ambiguous inline prose.
func ParseVerification(body string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if !verificationHeading.MatchString(strings.TrimSpace(line)) {
			continue
		}
		first := ""
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || strings.HasPrefix(t, ">") {
				continue
			}
			if anotherHeading.MatchString(t) {
				break
			}
			if strings.HasPrefix(t, "```") {
				lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
				if lang != "" && lang != "sh" && lang != "bash" {
					return "", fmt.Errorf("verification fence must use sh or bash")
				}
				for k := j + 1; k < len(lines); k++ {
					if strings.TrimSpace(lines[k]) == "```" {
						return strings.Join(lines[j+1:k], "\n"), nil
					}
				}
				return "", fmt.Errorf("unclosed verification code fence")
			}
			if first == "" {
				first = t
			}
		}
		if first != "" {
			return parseInlineVerification(first)
		}
		return "", nil
	}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "- "), "* "))
		if strings.HasPrefix(strings.ToLower(t), "verification:") {
			command := strings.TrimSpace(t[len("verification:"):])
			if command != "" {
				return parseInlineVerification(command)
			}
			return ParseVerification("## Verification\n" + strings.Join(lines[i+1:], "\n"))
		}
	}
	return "", nil
}

func parseInlineVerification(command string) (string, error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(command)), "prerequisite:") {
		return "", fmt.Errorf("verification contains a prerequisite but no command; put the exact shell script in a fenced sh block")
	}
	if proseAfterCommand.MatchString(command) {
		return "", fmt.Errorf("ambiguous verification command; put the exact shell script in a fenced sh block")
	}
	return command, nil
}

// ExtractVerification returns the command when it can be parsed unambiguously.
func ExtractVerification(body string) string {
	command, _ := ParseVerification(body)
	return command
}
