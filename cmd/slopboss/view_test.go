package main

import (
	"reflect"
	"testing"
)

func TestDefaultViewSessionUsesBoardName(t *testing.T) {
	if got, want := defaultViewSession("/work/funscript-board"), "slopboss-funscript"; got != want {
		t.Fatalf("defaultViewSession = %q, want %q", got, want)
	}
}

func TestManagedSessionCommandEntersForegroundMode(t *testing.T) {
	got := managedSessionCommand("/usr/local/bin/slopboss", "/work/funscript-board", "claude")
	want := []string{"/usr/local/bin/slopboss", "--dir", "/work/funscript-board", "run", "--managed-foreground", "--provider", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managedSessionCommand = %#v, want %#v", got, want)
	}
}

func TestManagedSessionArgsCreatesOrReusesDetachedSession(t *testing.T) {
	command := []string{"/usr/bin/slopboss", "--dir", "/work/funscript-board", "run", "--managed-foreground"}
	got := managedSessionArgs("slopboss-funscript", "/work/funscript-board", command)
	want := []string{"new-session", "-Ad", "-s", "slopboss-funscript", "-c", "/work/funscript-board", "/usr/bin/slopboss", "--dir", "/work/funscript-board", "run", "--managed-foreground"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managedSessionArgs = %#v, want %#v", got, want)
	}
}
