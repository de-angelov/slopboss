package board

import (
	"testing"
)

func TestExtractVerification(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "simple inline",
			body: "Objective: foo\nVerification: npx vitest run tests/syntax.test.ts",
			want: "npx vitest run tests/syntax.test.ts",
		},
		{
			name: "inline current directory",
			body: "Verification: go test .",
			want: "go test .",
		},
		{
			name: "multiline fence with comments",
			body: "## Verification\n```sh\nset -e\n# check\ngo test .\n```\n## Next\ntext",
			want: "set -e\n# check\ngo test .",
		},
		{
			name: "section with code fence",
			body: "## Objective\nfoo\n\n## Verification\n```sh\nnpm test\n```\n",
			want: "npm test",
		},
		{
			name: "section with plain text",
			body: "## Objective\nfoo\n\n## Verification\nnpx vitest run tests/compiler.test.ts\n",
			want: "npx vitest run tests/compiler.test.ts",
		},
		{
			name: "empty or missing",
			body: "Objective: foo\nScope: bar",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractVerification(tt.body)
			if got != tt.want {
				t.Fatalf("ExtractVerification() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseVerificationRejectsAmbiguous(t *testing.T) {
	for _, body := range []string{"Verification: npm test. Check results.", "## Verification\n```sh\necho hi", "## Verification\n```python\nprint(1)\n```"} {
		if _, err := ParseVerification(body); err == nil {
			t.Errorf("expected error for %q", body)
		}
	}
}
