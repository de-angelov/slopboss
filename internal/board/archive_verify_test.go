package board

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestMainCommitClaimDisambiguatesBaseAndBranchClaims(t *testing.T) {
	oldBase := config.BaseBranch
	t.Cleanup(func() { config.BaseBranch = oldBase })

	tests := []struct {
		name, base, line, want string
	}{
		{"base before landed commit", "main", "Squash-merged into `main` as commit `cb27433`", "cb27433"},
		{"landed commit before base", "main", "landed commit `cb27433` on origin/main", "cb27433"},
		{"branch then main on same line", "main", "Pushed branch at commit `dd63d44`; squash-merged into `main` as commit `cb27433`", "cb27433"},
		{"branch claim only", "main", "Pushed branch `agent/1/foo` at commit `dd63d44`", ""},
		{"configured non-main base", "develop", "landed commit `cb27433` on origin/develop", "cb27433"},
		{"other base ignored", "develop", "landed commit `cb27433` on origin/main", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config.BaseBranch = test.base
			if got := mainCommitClaim(test.line); got != test.want {
				t.Fatalf("mainCommitClaim(%q) = %q, want %q", test.line, got, test.want)
			}
		})
	}
}

func TestArchivedCompletionsExtractMainCommitClaim(t *testing.T) {
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })

	data := `# ARCHIVE

## Done

### Replace summaries

Task ID: CR-147
Status: Done

Merge Notes:
- Pushed task branch ` + "`agent/1/cr-147`" + ` at commit ` + "`dd63d44`" + `.
- Squash-merged into ` + "`main`" + ` as commit ` + "`cb27433`" + ` and pushed ` + "`main`" + `.
`
	if err := os.WriteFile(config.ArchiveFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	completions := archivedCompletions()
	if len(completions) != 1 {
		t.Fatalf("archivedCompletions parsed %d entries, want 1", len(completions))
	}
	if completions[0].id != "CR-147" {
		t.Fatalf("id = %q, want CR-147", completions[0].id)
	}
	if completions[0].mainCommit != "cb27433" {
		t.Fatalf("mainCommit = %q, want cb27433", completions[0].mainCommit)
	}
}

func TestArchivedCompletionsIgnoresTaskBranchCommit(t *testing.T) {
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })

	data := `# ARCHIVE

## Done

### Branch only

Task ID: CR-200
Status: Done

Merge Notes:
- Pushed task branch ` + "`agent/1/cr-200`" + ` at commit ` + "`abc1234`" + `.
`
	if err := os.WriteFile(config.ArchiveFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	completions := archivedCompletions()
	if len(completions) != 1 {
		t.Fatalf("archivedCompletions parsed %d entries, want 1", len(completions))
	}
	if completions[0].mainCommit != "" {
		t.Fatalf("mainCommit = %q, want empty for task-branch-only commit", completions[0].mainCommit)
	}
}

func TestCompletedSetExcludesArchiveEntryWhenClaimedMainCommitIsMissing(t *testing.T) {
	oldArchive := config.ArchiveFile
	oldTeamLeadPath := config.TeamLeadPath
	oldContains := mainContainsCommit
	oldMerged := mergedMainIDs
	oldIssues := archiveIssues
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	config.TeamLeadPath = t.TempDir()
	mainContainsCommit = func(_ string, commit string) bool { return commit == "1111111" }
	mergedMainIDs = map[string]bool{}
	t.Cleanup(func() {
		config.ArchiveFile = oldArchive
		config.TeamLeadPath = oldTeamLeadPath
		mainContainsCommit = oldContains
		mergedMainIDs = oldMerged
		archiveIssues = oldIssues
	})

	data := `# ARCHIVE

## Done

### Good archive

Task ID: GOOD-01
Status: Done
- Squash-merged into ` + "`main`" + ` as commit ` + "`1111111`" + ` and pushed ` + "`main`" + `.

### Stale archive

Task ID: STALE-02
Status: Done
- Squash-merged into ` + "`main`" + ` as commit ` + "`2222222`" + ` and pushed ` + "`main`" + `.

### Legacy archive

Task ID: LEGACY-03
Status: Done
`
	if err := os.WriteFile(config.ArchiveFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	issues := RefreshArchiveCompletionIssues()
	if len(issues) != 1 || issues[0].TaskID != "STALE-02" {
		t.Fatalf("RefreshArchiveCompletionIssues = %#v, want one STALE-02 issue", issues)
	}

	done := CompletedSet()
	if !done["GOOD-01"] {
		t.Fatal("expected verified archive entry to count as done")
	}
	if done["STALE-02"] {
		t.Fatal("expected stale archive entry to be excluded from completed set")
	}
	if !done["LEGACY-03"] {
		t.Fatal("expected archive entry without a main commit claim to remain compatible")
	}
}
