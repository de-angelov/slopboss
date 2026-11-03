package coordination

import (
	"path/filepath"
	"testing"
)

func TestWriteReportRequiresValidEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	for _, r := range []Report{{Status: "idle"}, {Status: "unknown", Summary: "x"}, {Status: "blocked", Summary: " "}} {
		if err := WriteReport(path, r); err == nil {
			t.Fatalf("accepted %#v", r)
		}
	}
	want := Report{Status: "blocked", Summary: "board command failed"}
	if err := WriteReport(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReport(path)
	if err != nil || got != want {
		t.Fatalf("got %#v, %v", got, err)
	}
}
