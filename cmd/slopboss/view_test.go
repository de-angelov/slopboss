package main

import "testing"

func TestDefaultViewSessionUsesBoardName(t *testing.T) {
	if got, want := defaultViewSession("/work/funscript-board"), "slopboss-funscript"; got != want {
		t.Fatalf("defaultViewSession = %q, want %q", got, want)
	}
}
