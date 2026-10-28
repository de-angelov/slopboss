// Package provider abstracts the agent backend CLI (Codex, Claude Code,
// OpenCode) behind a common interface so the orchestrator loop can drive any of
// them. The backend is selected once at startup via the --provider flag; codex
// is the default.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
)

// sortedKeys returns the keys of m in sorted order, for deterministic --config
// flag ordering.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Provider abstracts the agent backend CLI behind a common interface.
type Provider interface {
	Name() string
	// Command builds the backend process for a session. The caller sets Dir,
	// Stdin (the prompt), and Stdout/Stderr (the monitor). maxTurns hard-caps the
	// backend's agentic turns (0 = unlimited); backends that cannot enforce a cap
	// ignore it.
	Command(ctx context.Context, model string, maxTurns int) *exec.Cmd
	// InteractiveCommand builds an interactive (TUI) backend process seeded with
	// prompt as the first message. The caller wires it to the real terminal
	// (os.Stdin/Stdout/Stderr). Unlike Command it must NOT put the child in its
	// own process group, so it stays in the terminal's foreground group and
	// receives keystrokes and Ctrl-C.
	InteractiveCommand(ctx context.Context, model string, prompt string) *exec.Cmd
	// ExperimentCommand builds a headless process for one experiment variant. The
	// caller sets Dir + Stdin (the prompt) and wires Stdout/Stderr (typically an
	// io.MultiWriter of a NewMonitor and the per-variant log file). Backend knobs
	// in spec that a backend does not support are ignored.
	ExperimentCommand(ctx context.Context, spec ExperimentSpec) *exec.Cmd
	// DefaultModel returns the model to run for the given role, or "" to defer to
	// the backend's own configured default.
	DefaultModel(role string) string
	// NewMonitor returns a fresh stdout/stderr parser for one session.
	NewMonitor() Monitor
}

// ExperimentSpec carries the per-variant knobs an experiment run passes to a
// backend. Model applies to every backend; Profile, Config, and LastMessageFile
// are codex-specific and ignored by backends that do not support them (Claude
// surfaces its final message through the event stream instead — see
// Monitor.FinalMessage).
type ExperimentSpec struct {
	Model           string
	Profile         string
	Config          map[string]string
	LastMessageFile string
}

// Monitor consumes a session's combined stdout/stderr and extracts token usage,
// a usage-limit signal, and (where the backend surfaces it in-stream) the final
// assistant message. It does not itself write anywhere else; callers tee output
// to the log via an io.MultiWriter.
type Monitor interface {
	Write(p []byte) (int, error)
	Breakdown() TokenBreakdown
	UsageLimited() bool
	FinalMessage() string
}

// SessionIDer is implemented by monitors that learn the backend's session ID from
// the event stream. The ID lets providers look up final usage out of band when
// the stream omits the terminal usage event.
type SessionIDer interface {
	SessionID() string
}

// SessionUsageFetcher is implemented by providers that can look up final session
// usage out of band (e.g. when the stream omits the terminal usage event).
type SessionUsageFetcher interface {
	FetchUsage(ctx context.Context, sessionID string) (TokenBreakdown, error)
}

// ErrorReporter is implemented by monitors that can surface a structured backend
// error message (e.g. OpenCode's error event).
type ErrorReporter interface {
	ErrorMessage() string
}

// FinalUsage returns the monitor's token breakdown, preferring an out-of-band
// fetch when the provider and monitor support it. Backends whose stream always
// carries the final usage (codex, claude) are unaffected.
func FinalUsage(ctx context.Context, p Provider, m Monitor) TokenBreakdown {
	if ider, ok := m.(SessionIDer); ok {
		if id := ider.SessionID(); id != "" {
			if fetcher, ok := p.(SessionUsageFetcher); ok {
				if usage, err := fetcher.FetchUsage(ctx, id); err == nil && usage.Total > 0 {
					return usage
				}
			}
		}
	}
	return m.Breakdown()
}

// newBackendCmd builds the backend process in its own process group and, when
// the context is cancelled, kills the entire group. Backends (codex/claude)
// spawn child shells, test runners, and build tools; without a group kill those
// grandchildren would be orphaned when a session is cancelled (board change) or
// the orchestrator shuts down. WaitDelay bounds how long Wait blocks after the
// context is cancelled before the group is force-killed.
func newBackendCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID targets the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}

// ByName resolves the --provider flag value to a backend.
func ByName(name string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "codex":
		return codexProvider{}, nil
	case "claude":
		return claudeProvider{}, nil
	case "opencode":
		return opencodeProvider{}, nil
	case "agy", "antigravity":
		return agyProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want %q, %q, %q, or %q)", name, "codex", "claude", "opencode", "agy")
	}
}

// ---------------------------------------------------------------------------
// Codex
// ---------------------------------------------------------------------------

type codexProvider struct{}

func (codexProvider) Name() string { return "codex" }

func (codexProvider) DefaultModel(role string) string {
	if role == config.TeamLeadRole {
		return "" // grooming uses codex's configured default
	}
	return config.DevAgentModel
}

func (codexProvider) NewMonitor() Monitor { return &codexOutputMonitor{} }

func (codexProvider) Command(ctx context.Context, model string, maxTurns int) *exec.Cmd {
	// --json makes codex emit its structured event stream on stdout, carrying
	// the per-turn token accounting parsed by codexOutputMonitor. codex exec has
	// no turn-cap flag, so maxTurns is enforced only via the prompt constraints
	// for this backend.
	_ = maxTurns
	// --skip-git-repo-check lets codex run in a non-git directory (e.g. the board
	// root, where the tech quiz writes TECH.md); in dev-agent workspaces it is a
	// harmless no-op.
	args := []string{"exec", "--sandbox", "danger-full-access", "--skip-git-repo-check", "--json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-")
	return newBackendCmd(ctx, "codex", args...)
}

func (codexProvider) InteractiveCommand(ctx context.Context, model string, prompt string) *exec.Cmd {
	// No `exec` subcommand -> interactive TUI; the positional prompt seeds the
	// first message.
	args := []string{"--sandbox", "danger-full-access"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)
	return exec.CommandContext(ctx, "codex", args...)
}

func (codexProvider) ExperimentCommand(ctx context.Context, spec ExperimentSpec) *exec.Cmd {
	// codex supports the full spec: --output-last-message captures the final
	// message, --profile and per-variant --config tune the run, and --json emits
	// the event stream the monitor parses for token accounting.
	// --skip-git-repo-check allows non-git working dirs (e.g. the board root used
	// by the tech interview).
	args := []string{"exec", "--sandbox", "danger-full-access", "--skip-git-repo-check", "--json"}
	if spec.LastMessageFile != "" {
		args = append(args, "--output-last-message", spec.LastMessageFile)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Profile != "" {
		args = append(args, "--profile", spec.Profile)
	}
	for _, key := range sortedKeys(spec.Config) {
		args = append(args, "--config", fmt.Sprintf("%s=%s", key, spec.Config[key]))
	}
	args = append(args, "-")
	return newBackendCmd(ctx, "codex", args...)
}

// ---------------------------------------------------------------------------
// Claude Code
// ---------------------------------------------------------------------------

type claudeProvider struct{}

func (claudeProvider) Name() string { return "claude" }

func (claudeProvider) DefaultModel(role string) string {
	if role == config.TeamLeadRole {
		return "" // grooming uses the user's configured claude default
	}
	return config.ClaudeDevModel
}

func (claudeProvider) NewMonitor() Monitor { return &claudeOutputMonitor{} }

func (claudeProvider) Command(ctx context.Context, model string, maxTurns int) *exec.Cmd {
	// -p runs headless; stream-json + verbose emit the structured event stream
	// parsed by claudeOutputMonitor; skip-permissions mirrors codex's
	// danger-full-access posture for unattended runs.
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if maxTurns > 0 {
		// Headless Claude Code caps agentic turns with --max-turns; the session
		// stops cleanly at the limit rather than looping indefinitely.
		args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	return newBackendCmd(ctx, "claude", args...)
}

func (claudeProvider) InteractiveCommand(ctx context.Context, model string, prompt string) *exec.Cmd {
	// No -p/--print -> interactive session; the positional prompt seeds the first
	// message.
	args := []string{"--dangerously-skip-permissions"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)
	return exec.CommandContext(ctx, "claude", args...)
}

func (claudeProvider) ExperimentCommand(ctx context.Context, spec ExperimentSpec) *exec.Cmd {
	// Claude runs headless with the same stream-json output the orchestrator uses,
	// so claudeOutputMonitor parses token usage and the final message from it.
	// Experiments run uncapped (real implementation work), and Profile/Config/
	// LastMessageFile have no Claude equivalent, so they are ignored.
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	return newBackendCmd(ctx, "claude", args...)
}

// ---------------------------------------------------------------------------
// OpenCode
// ---------------------------------------------------------------------------

type opencodeProvider struct{}

func (opencodeProvider) Name() string { return "opencode" }

func (opencodeProvider) DefaultModel(role string) string {
	// Defer to the user's configured OpenCode model (provider/model#variant).
	_ = role
	return ""
}

func (opencodeProvider) NewMonitor() Monitor { return &opencodeOutputMonitor{} }

func (opencodeProvider) Command(ctx context.Context, model string, maxTurns int) *exec.Cmd {
	// opencode run is headless; --format json emits the structured event stream
	// parsed by opencodeOutputMonitor; --auto approves permissions for unattended
	// runs. maxTurns has no opencode equivalent, so it is ignored. The prompt is
	// read from stdin (no positional message).
	_ = maxTurns
	args := []string{"run", "--format", "json", "--auto"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return newBackendCmd(ctx, "opencode", args...)
}

func (opencodeProvider) InteractiveCommand(ctx context.Context, model string, prompt string) *exec.Cmd {
	// No `run` subcommand -> interactive TUI; --prompt seeds the first message.
	// OpenCode's interactive CLI has no --model flag, so it defers to the user's
	// configured OpenCode model.
	_ = model
	args := []string{"--auto", "--prompt", prompt}
	return exec.CommandContext(ctx, "opencode", args...)
}

func (opencodeProvider) ExperimentCommand(ctx context.Context, spec ExperimentSpec) *exec.Cmd {
	// OpenCode runs headless with the same JSON output the orchestrator uses, so
	// opencodeOutputMonitor parses token usage and the final message from it.
	// Profile/Config/LastMessageFile have no OpenCode equivalent and are ignored
	// (final message comes from the event stream).
	args := []string{"run", "--format", "json", "--auto"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	return newBackendCmd(ctx, "opencode", args...)
}

func (opencodeProvider) FetchUsage(ctx context.Context, sessionID string) (TokenBreakdown, error) {
	if strings.TrimSpace(sessionID) == "" {
		return TokenBreakdown{}, fmt.Errorf("opencode session id is required")
	}
	cmd := exec.CommandContext(ctx, "opencode", "session", "export", sessionID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return TokenBreakdown{}, fmt.Errorf("opencode session export %s: %w: %s", sessionID, err, strings.TrimSpace(stderr.String()))
	}

	var doc struct {
		Info struct {
			Tokens struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		return TokenBreakdown{}, fmt.Errorf("parse opencode session export: %w", err)
	}

	t := doc.Info.Tokens
	cached := t.Cache.Read + t.Cache.Write
	input := t.Input + cached
	output := t.Output + t.Reasoning
	return TokenBreakdown{
		Input:           input,
		CachedInput:     cached,
		Output:          output,
		ReasoningOutput: t.Reasoning,
		Total:           input + output,
	}, nil
}

// ---------------------------------------------------------------------------
// Antigravity (agy)
// ---------------------------------------------------------------------------

type agyProvider struct{}

func (agyProvider) Name() string { return "agy" }

func (agyProvider) DefaultModel(role string) string {
	// Defer to the user's configured Antigravity model in settings.json.
	_ = role
	return ""
}

func (agyProvider) NewMonitor() Monitor { return &agyOutputMonitor{} }

func (agyProvider) Command(ctx context.Context, model string, maxTurns int) *exec.Cmd {
	_ = maxTurns
	args := []string{"--output-format", "stream-json", "--dangerously-skip-permissions"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return newBackendCmd(ctx, "agy", args...)
}

func (agyProvider) InteractiveCommand(ctx context.Context, model string, prompt string) *exec.Cmd {
	args := []string{"--dangerously-skip-permissions"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-i", prompt)
	return exec.CommandContext(ctx, "agy", args...)
}

func (agyProvider) ExperimentCommand(ctx context.Context, spec ExperimentSpec) *exec.Cmd {
	args := []string{"--output-format", "stream-json", "--dangerously-skip-permissions"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	return newBackendCmd(ctx, "agy", args...)
}
