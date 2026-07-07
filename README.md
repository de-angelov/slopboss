<div align="center">

# 🧑‍💼 slopboss

**An autonomous orchestrator for board-driven, multi-agent software development.**

slopboss reads a plain-Markdown task board, spins up a team of AI coding agents
(Codex or Claude Code), and keeps the running sessions continuously reconciled
with the board — assigning work, isolating each agent in its own git workspace,
and shutting sessions down as tasks land on `main`.

[![Go Reference](https://img.shields.io/badge/go-reference-00ADD8?logo=go&logoColor=white)](https://pkg.go.dev/github.com/de-angelov/slopboss)
[![Go Version](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Build](https://img.shields.io/badge/build-passing-brightgreen)](#-development)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-ff69b4.svg)](#-contributing)

[Quickstart](#-quickstart) •
[How it works](#-how-it-works) •
[Commands](#-commands) •
[The board](#-the-board) •
[Experiments](#-experiments) •
[Architecture](#-architecture)

</div>

---

## ✨ Overview

Most "AI agent" tools give you a single chat loop. **slopboss gives you a team.**

You describe work as Markdown tasks. A **Team Lead** agent grooms the backlog and
promotes the highest-priority items into per-agent lanes. Multiple **Dev Agents**
pick up their assigned lane, implement the task on a dedicated git branch inside
an isolated workspace, verify it, and archive it when done. slopboss is the loop
in the middle: it polls the board, diffs it against the sessions that are actually
running, and starts or cancels agent processes until reality matches the board —
then repeats, forever, until you stop it.

Everything the agents coordinate through is a **file you can read, edit, and
commit** — no database, no hidden state.

### Why slopboss?

- 🗂️ **Board is the source of truth** — `BACKLOG.md`, `TASKS.md`, and `ARCHIVE.md`
  are human-readable Markdown. Edit the board and the running fleet converges to it.
- 🔁 **Reconcile, don't fire-and-forget** — a control loop keeps live sessions in
  sync with the board every few seconds, so hand edits and agent edits both take effect.
- 🧑‍💻 **A real division of labor** — one Team Lead grooms and prioritizes; N Dev
  Agents execute in parallel, each in its own cloned workspace and branch.
- 🔌 **Pluggable backends** — run on **Codex** (default) or **Claude Code** with a flag.
- 🧪 **Built-in experiments** — A/B different models and prompts on the same ticket
  in isolated worktrees and get a token/diff report.
- 📺 **Live TUI** — watch every agent, its current task, status, and token usage in real time.
- 🛟 **Resilient by design** — merge detection, retry/backoff on failures, and
  usage-limit cooldowns are all handled for you.

---

## 📦 Installation

### Go install

```bash
go install github.com/de-angelov/slopboss/cmd/slopboss@latest
```

### From source

```bash
git clone https://github.com/de-angelov/slopboss.git
cd slopboss
go build -o slopboss ./cmd/slopboss
```

> **Requirements:** Go **1.26+**, `git`, and at least one agent backend CLI on your
> `PATH` — [Codex](https://github.com/openai/codex) (default) or
> [Claude Code](https://docs.claude.com/en/docs/claude-code) (`--provider claude`).

---

## 🚀 Quickstart

```bash
# 1. Run the setup wizard — it prompts for the board directory, product repo,
#    base branch, dev-agent count, and backend, then clones workspaces, creates
#    the base branch if missing, scaffolds the board, and runs an adaptive tech
#    interview (the agent asks, you answer, it writes TECH.md). Pass any answer
#    as a flag to skip its prompt (e.g. --repo ... --agents 2 --skip-interview).
slopboss setup

# 2. Add and prioritize work with an interactive Team Lead session
slopboss groom

# 3. Start the orchestrator — it reconciles the board with running agents
slopboss run
```

`run` opens a live TUI and keeps working until you press `Ctrl-C` (or send
`SIGTERM`), at which point it cancels in-flight sessions and exits cleanly.

> **Where to run it:** slopboss resolves which board to use in this order —
> `--dir <board>` if given, else the current directory if it's a board, else the
> **active board recorded by `setup`** (in `~/.config/slopboss/config.md`). So
> after `setup`, bare commands like `slopboss experiment groom` work from
> anywhere and print which board they picked; use `--dir` to target a different
> one.

Switch backends at any time:

```bash
slopboss run --provider claude
```

---

## 🧠 How it works

```
                    ┌────────────────────────────────────────────────┐
                    │                   THE BOARD                    │
                    │  BACKLOG.md   TASKS.md   ARCHIVE.md  (+ docs)  │
                    └────────────────▲──────────────┬────────────────┘
                        grooms /     │              │  polls every 10s
                        promotes     │              │
              ┌──────────────────────┴────┐   ┌─────▼──────────────────┐
              │      Team Lead Agent      │   │   slopboss reconcile   │
              │  (repo-tl workspace)      │   │         loop           │
              └───────────────────────────┘   └───────────┬────────────┘
                                                          │ start / cancel
                                          ┌───────────────┼────────────────┐
                                          ▼               ▼                ▼
                                   ┌────────────┐  ┌────────────┐   ┌────────────┐
                                   │ Dev Agent 1│  │ Dev Agent 2│ … │ Dev Agent N│
                                   │repo-agent-1│  │repo-agent-2│   │repo-agent-N│
                                   └─────┬──────┘  └─────┬──────┘   └─────┬──────┘
                                         │ branch + PR   │                │
                                         └───────────────┴────────────────┘
                                                    merges to main
```

1. **Poll** — every `10s`, slopboss reads the board files.
2. **Reconcile** — it compares each task's assigned lane against the sessions that
   are actually running and computes the difference.
3. **Converge** — it starts a backend session for any assigned-but-not-running
   task and cancels sessions whose task has moved, changed, or already merged to
   `main`. Cancellation is debounced by a grace period so completion-time board
   rewrites don't kill live work.
4. **Repeat** — the TUI refreshes continuously; the loop runs until interrupted.

Because a task that merged to `main` but was never archived still counts as done,
dependent tasks never get stuck behind a session that was cancelled mid-completion.

---

## 🕹️ Commands

| Command | What it does |
| --- | --- |
| `slopboss setup` | Interactive wizard (like `npm init`): prompts for the board directory, product repo, base branch, dev-agent count, and backend; clones the workspaces, creates the base branch if missing, scaffolds the board files + `CONFIG.md`, then runs an **adaptive tech interview** — the backend asks follow-up questions (slopboss relays each Q&A) and writes `TECH.md`. Any answer given as a flag isn't prompted, so it can run fully non-interactively. |
| `slopboss run` | Run the autonomous reconcile loop with a live TUI until interrupted. |
| `slopboss groom` | Launch a one-off **interactive** Team Lead session to capture and prioritize tasks in `BACKLOG.md`. |
| `slopboss experiment groom` | Design an experiment interactively with the Team Lead, written to `EXPERIMENT.md`. |
| `slopboss experiment run` | Run the experiment (`EXPERIMENT.md` at the repo root by default; `--config` to override) and produce a report. |
| `slopboss version` | Print the slopboss version. |

### Common flags

| Flag | Commands | Default | Description |
| --- | --- | --- | --- |
| `--provider` | `run`, `groom`, `setup`, `experiment run`, `experiment groom` | persisted (`codex`) | Agent backend: `codex` or `claude`. `setup` **persists** your choice to `CONFIG.md`, and the other commands default to it. |
| `--dir` | **all commands** (global) | current directory | Board directory to operate in — run any command against a board from anywhere without `cd`. |
| `--repo` | `setup` | — (prompted) | Product repository to clone into each workspace. |
| `--ssh-url` | `setup` | `--repo` | Origin URL to set after cloning. |
| `--branch` | `setup` | `main` (prompted) | Base/integration branch agents target; **created if the repo doesn't have it**, and persisted to `CONFIG.md`. |
| `--agents` | `setup` | `2` | Number of Dev Agent workspaces to create. |
| `--skip-interview` | `setup` | `false` | Skip the tech-stack interview; leave a placeholder `TECH.md`. |
| `--config` | `experiment run` | `EXPERIMENT.md` (repo root) | Override the experiment file to run (`.md` or `.json`). |
| `--dry-run` | `experiment run` | `false` | Prepare prompts and worktrees without invoking the backend. |

> ℹ️ `slopboss run` discovers how many Dev Agents to drive by counting the
> `repo-agent-*` workspaces created during `setup`.

---

## 🗂️ The board

slopboss treats a handful of Markdown files at your repo root as the entire
coordination surface. `setup` scaffolds minimal starters you customize per project:

| File | Role |
| --- | --- |
| `BACKLOG.md` | Prioritized backlog. The Team Lead grooms this and promotes the top task into a Dev Agent lane. |
| `TASKS.md` | The active board — one lane per Dev Agent with the task currently in flight. |
| `ARCHIVE.md` | Completed-work history. Not loaded into normal agent prompts. |
| `AGENTS.md` | Common rules every agent must follow. |
| `DEV_AGENT.md` | Role instructions for Dev Agents. |
| `TEAM_LEAD_AGENT.md` | Role instructions for the Team Lead. |
| `TECH.md` | Project tech context shared with agents. |
| `CONFIG.md` | slopboss's own configuration (product repo, base branch, backend, dev-agent count) as `- Key: Value` bullets — written by the setup wizard, read on startup, and used to pre-fill the wizard on re-run. |

A task is a small block of Markdown with a title, `Owner`, `Branch`, `Status`,
and a free-form body describing the work — for example:

```markdown
### Add dark-mode toggle to settings
Owner: (unassigned)
Branch: agent/1/dark-mode-toggle
Status: Backlog

Add a theme toggle to the settings page that persists the choice to localStorage
and respects the OS `prefers-color-scheme` on first load.
```

The Team Lead moves it into a lane in `TASKS.md`; a Dev Agent implements it on its
branch and rewrites `TASKS.md`/`ARCHIVE.md` as the final step when the work is done.

---

## 🧪 Experiments

Compare models, prompts, **and backends** head-to-head on the same ticket. Each
variant runs in an **isolated git worktree** so diffs never collide, and slopboss
collects token and diff metrics into a `report.md`.

You don't hand-write config — **the Team Lead helps you design it**, the same way
`slopboss groom` curates the backlog:

```bash
# 1. Design the experiment interactively; writes EXPERIMENT.md at the repo root
slopboss experiment groom

# 2. Run it — picks up EXPERIMENT.md automatically (dry-run first to preview
#    prompts/worktrees without spending tokens)
slopboss experiment run --dry-run
slopboss experiment run

# Point at a different file only when you want to:
slopboss experiment run --config experiments/example.md
```

Experiments are defined in the same human-friendly Markdown as the board.
Structured settings are `- Key: Value` bullets, each `### section` under
`## Variants` is one variant, and prose is ignored — so a mistyped key is a real
error, not a silent no-op:

```markdown
# Experiment: codex-vs-claude

- Task: Add dark-mode toggle
- Prompt mode: bounded

## Variants

### codex-baseline
- Provider: codex
- Model: gpt-5.5

### claude-baseline
- Provider: claude
- Model: claude-sonnet-5
```

Experiments run through the same `Provider` abstraction as the orchestrator, so
either backend works. Provider selection resolves per variant with a clear
precedence — **variant `Provider` → file `Provider` → `--provider` flag** — so a
single run can pit Codex against Claude. Token usage and the final-response
summary are parsed live from each backend's own event stream (codex `--json`,
Claude `stream-json`); codex-only knobs (`Profile`, `Config`) are ignored by
Claude. On completion slopboss prints the path to the generated report, e.g.
`experiments/<run>/report.md`, whose table includes a **Backend** column.

> JSON configs are still accepted by `experiment run` (`--config foo.json`) for
> automation; the Markdown format is the authoring default. A ready-to-edit
> example lives at [`experiments/example.md`](experiments/example.md).

---

## 🏗️ Architecture

slopboss is a single Go binary built on [`cobra`](https://github.com/spf13/cobra)
for its CLI and [`tview`](https://github.com/rivo/tview)/[`tcell`](https://github.com/gdamore/tcell)
for its TUI. It follows the standard `cmd/` + `internal/` layout, with each
internal package owning one concern and the dependencies pointing strictly one
way (foundation → domain → orchestration):

```
slopboss/
├── cmd/slopboss/           # CLI entrypoint + cobra command wiring
└── internal/
    ├── config/             # paths, roles, tuning constants, repo-root discovery
    ├── logx/               # shared append-only event log
    ├── git/                # git plumbing for workspaces & origin/main
    ├── board/              # Task model, board parsing, queries, merge detection
    ├── provider/           # Codex / Claude backend abstraction + monitors
    ├── prompt/             # cache-friendly prompt assembly
    ├── experiment/         # model/prompt A/B experiment runner
    ├── orchestrator/       # reconcile loop, scheduler, session lifecycle, groom
    ├── tui/                # live terminal dashboard (implements orchestrator.UI)
    └── setup/              # workspace cloning + board scaffolding
```

The dependency graph is acyclic by design:

```
config ← logx ← git ← board ← prompt ← experiment
                        ↑         ↑
        provider ───────┴─────────┴──→ orchestrator ← tui
cmd/slopboss wires everything together
```

The orchestrator owns all mutable session state behind a single mutex and exposes
a read-only `Snapshot` so the TUI renders without touching that state directly —
which is why `tui` depends on `orchestrator`, and never the reverse.

Key design choices:

- **Files over state.** The board is the only source of truth; slopboss holds no
  database and can be restarted at any time without losing progress.
- **Reconcile over dispatch.** Rather than firing tasks once, the loop continuously
  drives observed state toward the board's desired state.
- **Bounded agentic turns.** Grooming sessions are capped to keep token growth from
  ballooning superlinearly; hitting the cap simply resumes from the updated board
  next poll, so nothing is lost.

---

## 🛠️ Development

```bash
go build ./...     # compile
go test ./...      # run the test suite
go vet ./...       # static checks
```

Build a versioned binary:

```bash
go build -ldflags "-X main.version=$(git describe --tags --always)" -o slopboss ./cmd/slopboss
```

---

## 🤝 Contributing

Issues and pull requests are welcome! Please make sure `go test ./...` and
`go vet ./...` pass, keep changes focused, and describe the behavior your change
affects.

---

## 📄 License

Released under the [MIT License](LICENSE).
