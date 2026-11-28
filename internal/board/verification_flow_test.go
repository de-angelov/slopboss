package board

import "testing"

func TestVerificationPrefersExplicitFenceAfterPrerequisite(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{"## Verification\nPrerequisite: dependencies are installed.\n```sh\nset -e\nnpm run build\nnpm test\n```\nInspect output.\n", "set -e\nnpm run build\nnpm test"},
		{"Verification:\nPrerequisite: dependencies are installed.\n```sh\ngo test ./internal/board\n```\n", "go test ./internal/board"},
	} {
		got, err := ParseVerification(test.body)
		if err != nil || got != test.want {
			t.Errorf("ParseVerification() = %q, %v; want %q", got, err, test.want)
		}
	}
}

func TestVerificationRejectsPrerequisiteWithoutCommand(t *testing.T) {
	for _, body := range []string{
		"## Verification\nPrerequisite: dependencies are installed.",
		"Verification: prerequisite: dependencies are installed.",
	} {
		if _, err := ParseVerification(body); err == nil {
			t.Errorf("ParseVerification(%q) succeeded; want prerequisite error", body)
		}
	}
}
