package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceDirs(t *testing.T) {
	got := workspaceDirs(3)
	want := []string{"repo-tl", "repo-agent-1", "repo-agent-2", "repo-agent-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("workspaceDirs(3) = %v, want %v", got, want)
	}

	if only := workspaceDirs(1); len(only) != 2 || only[0] != "repo-tl" || only[1] != "repo-agent-1" {
		t.Fatalf("workspaceDirs(1) = %v, want [repo-tl repo-agent-1]", only)
	}
}

func TestOptionsDefaults(t *testing.T) {
	// RepoSSHURL defaults to RepoURL, and DevAgents defaults to DefaultDevAgents.
	o := Options{RepoURL: "git@example.com:acme/app.git"}.withDefaults()
	if o.RepoSSHURL != o.RepoURL {
		t.Fatalf("RepoSSHURL should default to RepoURL: %+v", o)
	}
	if o.DevAgents != DefaultDevAgents {
		t.Fatalf("DevAgents default = %d, want %d", o.DevAgents, DefaultDevAgents)
	}

	if got := (Options{DevAgents: 4}).withDefaults().DevAgents; got != 4 {
		t.Fatalf("explicit DevAgents overridden: got %d, want 4", got)
	}

	// An explicit SSH URL is preserved.
	if o := (Options{RepoURL: "https://x/y", RepoSSHURL: "git@x:y.git"}).withDefaults(); o.RepoSSHURL != "git@x:y.git" {
		t.Fatalf("explicit RepoSSHURL overridden: %+v", o)
	}

	// BaseBranch defaults to main, and an explicit value is preserved.
	if o := (Options{RepoURL: "x"}).withDefaults(); o.BaseBranch != "main" {
		t.Fatalf("BaseBranch default = %q, want main", o.BaseBranch)
	}
	if o := (Options{RepoURL: "x", BaseBranch: "develop"}).withDefaults(); o.BaseBranch != "develop" {
		t.Fatalf("explicit BaseBranch overridden: %q", o.BaseBranch)
	}
}

func TestTechSynthesisPromptForbidsContinuingInterview(t *testing.T) {
	prompt := techSynthesisPrompt(`Q: should we standardize on Next.js, Redis, and Stripe?
A: yes`)

	for _, want := range []string{
		"Do not ask follow-up questions",
		"Treat the transcript as complete",
		"choose concrete defaults",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("tech synthesis prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestValidateSynthesizedTechFileRejectsFollowUpQuestion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TECH.md")
	content := `# TECH

The product workspace is greenfield and TECH.md does not define a stack yet, so the first
blocker is choosing the app skeleton before I split this into ready tasks.

Question: should we standardize on a single Next.js TypeScript app with App Router, API routes
for backend endpoints, Redis for vote totals, and Stripe Checkout/webhooks for paid votes?

Recommended approach: yes.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateSynthesizedTechFile(path); err == nil {
		t.Fatal("expected follow-up-question TECH.md to be rejected")
	}
}

func TestValidateSynthesizedTechFileRejectsDefaultPlaceholder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TECH.md")
	if err := os.WriteFile(path, []byte(boardTemplates["TECH.md"]), 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateSynthesizedTechFile(path); err == nil {
		t.Fatal("expected default placeholder TECH.md to be rejected")
	}
}

func TestValidateSynthesizedTechFileAcceptsConcreteStack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TECH.md")
	content := `# TECH

Country Rank is a single Next.js TypeScript app using App Router for pages and route handlers for backend endpoints.

## Technology Stack

- Language / runtime: TypeScript on Node.js 20.
- Framework(s): Next.js App Router.
- Package manager: pnpm.
- Backend / server: Next.js route handlers.
- Database / storage: Redis.
- Key libraries: Stripe SDK, React Testing Library, Vitest.

## Architecture

Pages and server endpoints live in one app.

## Coding Standards

- Use strict TypeScript.

## Commands

- Install: pnpm install
- Test: pnpm test
- Build / typecheck: pnpm build && pnpm typecheck
- Lint / format: pnpm lint && pnpm format
- Other (migrations, codegen, seeds): none

## Testing

- Test stack: Vitest and Playwright.
- What must be covered (and the coverage/verification bar): unit tests for logic and e2e tests for voting flows.
- Where tests live: colocated *.test.ts files and tests/e2e.

## Conventions

- Directory layout: app/, components/, lib/, tests/.
- Definition of done: tests and build pass.
- Avoid: untyped API payloads.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateSynthesizedTechFile(path); err != nil {
		t.Fatalf("expected concrete TECH.md to be accepted: %v", err)
	}
}
