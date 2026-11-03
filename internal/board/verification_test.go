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
			name: "inline with trailing period",
			body: "Objective: foo\nVerification: npx vitest run tests/syntax.test.ts.",
			want: "npx vitest run tests/syntax.test.ts",
		},
		{
			name: "inline with full-suite diagnostic",
			body: "Objective: foo\nVerification: npx vitest run tests/formatter.test.ts --reporter=dot; npm run build. Full-suite diagnostic: npm test.",
			want: "npx vitest run tests/formatter.test.ts --reporter=dot; npm run build",
		},
		{
			name: "inline with diagnostic full suite wording",
			body: "Objective: foo\nVerification: npx vitest run tests/formatter.test.ts tests/parser.test.ts --reporter=dot; npm run build. Diagnostic full suite: npm test.",
			want: "npx vitest run tests/formatter.test.ts tests/parser.test.ts --reporter=dot; npm run build",
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
