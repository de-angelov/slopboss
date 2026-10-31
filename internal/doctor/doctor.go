// Package doctor provides read-only preflight checks for a slopboss board.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/de-angelov/slopboss/internal/board"
)

type Finding struct{ Level, Check, Message string }
type Options struct {
	Root, Provider string
	DevAgents      int
	LookPath       func(string) (string, error)
	Run            func(name string, args ...string) error
	Stat           func(string) (os.FileInfo, error)
	ReadFile       func(string) ([]byte, error)
}

func (o Options) defaults() Options {
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Run == nil {
		o.Run = func(n string, a ...string) error { return exec.Command(n, a...).Run() }
	}
	if o.Stat == nil {
		o.Stat = os.Stat
	}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	return o
}

// Check performs all probes without modifying the board or machine state.
func Check(opts Options) []Finding {
	o := opts.defaults()
	out := []Finding{}
	add := func(level, check, msg string) { out = append(out, Finding{level, check, msg}) }
	files := []struct{ name, path string }{{"backlog", filepath.Join(o.Root, "BACKLOG.md")}, {"tasks", filepath.Join(o.Root, "TASKS.md")}, {"archive", filepath.Join(o.Root, "ARCHIVE.md")}}
	all := []board.Task{}
	allIDs := map[string]bool{}
	idPattern := regexp.MustCompile(`(?im)^\s*(?:[-*•]\s*)?(?:\*\*)?Task ID(?:\*\*)?\s*:\s*([^\s]+)`)
	for _, f := range files {
		data, err := o.ReadFile(f.path)
		if err != nil {
			add("FAIL", f.path, "missing or unreadable; restore/create this board file")
			continue
		}
		tasks := board.ParseTasks(string(data))
		for _, match := range idPattern.FindAllStringSubmatch(string(data), -1) {
			allIDs[strings.TrimSpace(match[1])] = true
		}
		all = append(all, tasks...)
		for _, t := range tasks {
			if _, err := board.ValidateLifecycle(t, f.name); err != nil {
				add("FAIL", t.ID, "invalid lifecycle metadata for task "+t.Title+": "+err.Error()+"; correct Owner, Status, and section in "+f.path)
			}
		}
	}
	active := map[string]bool{}
	for _, t := range all {
		if t.Status == "In Progress" {
			if active[t.Owner] {
				add("FAIL", t.Owner, "duplicate active lane; leave at most one In Progress task in this lane")
			}
			active[t.Owner] = true
		}
	}
	for _, t := range all {
		for _, dep := range dependencyIDs(t.Dependencies) {
			if !allIDs[dep] {
				add("FAIL", t.ID, "unresolved dependency "+dep+"; add the task to the board or correct Dependencies in the task entry")
			}
		}
	}
	add("PASS", "board", "board files and lifecycle metadata checked")
	if o.Provider == "" {
		o.Provider = "codex"
	}
	if _, err := o.LookPath(o.Provider); err != nil {
		add("FAIL", o.Provider, "provider executable unavailable; install it or put it on PATH")
	} else {
		add("PASS", o.Provider, "provider executable found")
		args := []string{"login", "status"}
		if o.Provider == "claude" {
			args = []string{"auth", "status"}
		} else if o.Provider == "opencode" {
			args = []string{"auth", "list"}
		} else if o.Provider == "agy" || o.Provider == "antigravity" {
			args = []string{"auth", "status"}
		}
		if err := o.Run(o.Provider, args...); err != nil {
			add("FAIL", o.Provider, "authentication probe failed; run `"+o.Provider+" login` and complete sign-in")
		} else {
			add("PASS", o.Provider, "authentication probe passed")
		}
	}
	gitPath, err := o.LookPath("git")
	if err != nil {
		add("FAIL", "git", "git executable unavailable; install git and put it on PATH")
	} else {
		if err := o.Run(gitPath, "-C", o.Root, "remote", "get-url", "origin"); err != nil {
			add("FAIL", "origin", "origin remote unavailable; run `git remote add origin <url>`")
		} else {
			add("PASS", "origin", "origin remote found")
			if err := o.Run(gitPath, "-C", o.Root, "ls-remote", "--exit-code", "origin", "HEAD"); err != nil {
				add("FAIL", "origin", "remote authentication or connectivity failed; configure access, then run `git ls-remote origin HEAD`")
			} else {
				add("PASS", "origin", "remote access passed")
			}
		}
	}
	for _, p := range append([]string{filepath.Join(o.Root, "workspaces", "repo-tl")}, agentPaths(o.Root, o.DevAgents)...) {
		info, err := o.Stat(p)
		if err != nil || !info.IsDir() {
			add("FAIL", p, "workspace missing; create/clone this workspace with `slopboss setup`")
		} else {
			add("PASS", p, "workspace exists")
		}
	}
	return out
}
func dependencyIDs(s string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		v = strings.TrimSpace(v)
		if v != "" && !strings.EqualFold(v, "none") {
			out = append(out, v)
		}
	}
	return out
}
func agentPaths(root string, n int) []string {
	var p []string
	for i := 1; i <= n; i++ {
		p = append(p, filepath.Join(root, "workspaces", fmt.Sprintf("repo-agent-%d", i)))
	}
	return p
}
