package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSetupBoardDirAvoidsSlopbossSourceCheckout(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "slopboss")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module github.com/de-angelov/slopboss\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := defaultSetupBoardDir(source)
	want := filepath.Join(parent, "slopboss-board")
	if got != want {
		t.Fatalf("defaultSetupBoardDir(%q) = %q, want %q", source, got, want)
	}
}

func TestDefaultSetupBoardDirUsesCurrentDirectoryOutsideSourceCheckout(t *testing.T) {
	cwd := t.TempDir()

	if got := defaultSetupBoardDir(cwd); got != cwd {
		t.Fatalf("defaultSetupBoardDir(%q) = %q, want current directory", cwd, got)
	}
}
