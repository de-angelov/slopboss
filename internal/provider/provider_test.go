package provider

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestClaudeCommandAppliesMaxTurns(t *testing.T) {
	cmd := (claudeProvider{}).Command(context.Background(), "", config.TeamLeadMaxTurns)
	if !slices.Contains(cmd.Args, "--max-turns") {
		t.Fatalf("claude command missing --max-turns: %v", cmd.Args)
	}
	i := slices.Index(cmd.Args, "--max-turns")
	if i+1 >= len(cmd.Args) || cmd.Args[i+1] != strconv.Itoa(config.TeamLeadMaxTurns) {
		t.Fatalf("claude --max-turns value = %v, want %d", cmd.Args, config.TeamLeadMaxTurns)
	}
}

func TestClaudeCommandOmitsMaxTurnsWhenUncapped(t *testing.T) {
	cmd := (claudeProvider{}).Command(context.Background(), "", 0)
	if slices.Contains(cmd.Args, "--max-turns") {
		t.Fatalf("claude command should not cap turns when maxTurns=0: %v", cmd.Args)
	}
}

func TestCodexCommandIgnoresMaxTurns(t *testing.T) {
	// codex exec has no turn-cap flag; passing maxTurns must not inject stray args.
	cmd := (codexProvider{}).Command(context.Background(), "", config.TeamLeadMaxTurns)
	joined := strings.Join(cmd.Args, " ")
	if strings.Contains(joined, "max-turns") {
		t.Fatalf("codex command must not carry a turn-cap flag: %v", cmd.Args)
	}
}

func TestByName(t *testing.T) {
	cases := map[string]string{
		"":            "codex",
		"codex":       "codex",
		"Codex":       "codex",
		"claude":      "claude",
		" claude ":    "claude",
		"opencode":    "opencode",
		"OpenCode":    "opencode",
		" opencode ":  "opencode",
		"agy":         "agy",
		"Agy":         "agy",
		" agy ":       "agy",
		"antigravity": "agy",
	}
	for input, want := range cases {
		p, err := ByName(input)
		if err != nil {
			t.Fatalf("ByName(%q) error: %v", input, err)
		}
		if p.Name() != want {
			t.Fatalf("ByName(%q).Name() = %q, want %q", input, p.Name(), want)
		}
	}

	if _, err := ByName("unknown-backend"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestProviderDefaultModels(t *testing.T) {
	if got := (codexProvider{}).DefaultModel(config.DevAgent1Role); got != config.DevAgentModel {
		t.Fatalf("codex dev model = %q, want %q", got, config.DevAgentModel)
	}
	if got := (codexProvider{}).DefaultModel(config.TeamLeadRole); got != "" {
		t.Fatalf("codex team-lead model = %q, want configured default (empty)", got)
	}
	if got := (claudeProvider{}).DefaultModel(config.DevAgent1Role); got != config.ClaudeDevModel {
		t.Fatalf("claude dev model = %q, want %q", got, config.ClaudeDevModel)
	}
	if got := (claudeProvider{}).DefaultModel(config.TeamLeadRole); got != "" {
		t.Fatalf("claude team-lead model = %q, want configured default (empty)", got)
	}
	if got := (opencodeProvider{}).DefaultModel(config.DevAgent1Role); got != "" {
		t.Fatalf("opencode dev model = %q, want empty (user configured default)", got)
	}
	if got := (opencodeProvider{}).DefaultModel(config.TeamLeadRole); got != "" {
		t.Fatalf("opencode team-lead model = %q, want empty (user configured default)", got)
	}
	if got := (agyProvider{}).DefaultModel(config.DevAgent1Role); got != "" {
		t.Fatalf("agy dev model = %q, want empty (user configured default)", got)
	}
	if got := (agyProvider{}).DefaultModel(config.TeamLeadRole); got != "" {
		t.Fatalf("agy team-lead model = %q, want empty (user configured default)", got)
	}
}

func TestDevAgentModelUsesBenchmarkWinner(t *testing.T) {
	if config.DevAgentModel != "gpt-5.5" {
		t.Fatalf("DevAgentModel = %q, want gpt-5.5", config.DevAgentModel)
	}
}

func TestInteractiveCommandsAreNotHeadless(t *testing.T) {
	promptText := "seed prompt"

	hasArg := func(args []string, want string) bool {
		return slices.Contains(args, want)
	}

	codex := (codexProvider{}).InteractiveCommand(t.Context(), "gpt-5.5", promptText)
	if hasArg(codex.Args, "exec") || hasArg(codex.Args, "--json") {
		t.Fatalf("codex interactive command must not be headless: %v", codex.Args)
	}
	if !hasArg(codex.Args, "--sandbox") || !hasArg(codex.Args, "danger-full-access") {
		t.Fatalf("codex interactive command missing sandbox flag: %v", codex.Args)
	}
	if codex.Args[len(codex.Args)-1] != promptText {
		t.Fatalf("codex interactive command must end with the seed prompt: %v", codex.Args)
	}

	claude := (claudeProvider{}).InteractiveCommand(t.Context(), "", promptText)
	if hasArg(claude.Args, "-p") || hasArg(claude.Args, "--print") || hasArg(claude.Args, "stream-json") {
		t.Fatalf("claude interactive command must not be headless: %v", claude.Args)
	}
	if !hasArg(claude.Args, "--dangerously-skip-permissions") {
		t.Fatalf("claude interactive command missing skip-permissions flag: %v", claude.Args)
	}
	if claude.Args[len(claude.Args)-1] != promptText {
		t.Fatalf("claude interactive command must end with the seed prompt: %v", claude.Args)
	}

	opencode := (opencodeProvider{}).InteractiveCommand(t.Context(), "anthropic/claude-3-7-sonnet", promptText)
	if hasArg(opencode.Args, "run") || hasArg(opencode.Args, "--format") {
		t.Fatalf("opencode interactive command must not be headless: %v", opencode.Args)
	}
	if !hasArg(opencode.Args, "--auto") {
		t.Fatalf("opencode interactive command missing --auto flag: %v", opencode.Args)
	}
	if !hasArg(opencode.Args, "--prompt") {
		t.Fatalf("opencode interactive command missing --prompt flag: %v", opencode.Args)
	}
	if hasArg(opencode.Args, "--model") {
		t.Fatalf("opencode interactive command must not pass --model: %v", opencode.Args)
	}

	agy := (agyProvider{}).InteractiveCommand(t.Context(), "Gemini 3.8 Flash (Medium)", promptText)
	if hasArg(agy.Args, "-p") || hasArg(agy.Args, "--print") || hasArg(agy.Args, "stream-json") {
		t.Fatalf("agy interactive command must not be headless: %v", agy.Args)
	}
	if !hasArg(agy.Args, "--dangerously-skip-permissions") {
		t.Fatalf("agy interactive command missing skip-permissions flag: %v", agy.Args)
	}
	if !hasArg(agy.Args, "-i") {
		t.Fatalf("agy interactive command missing -i flag: %v", agy.Args)
	}
	if agy.Args[len(agy.Args)-1] != promptText {
		t.Fatalf("agy interactive command must end with seed prompt: %v", agy.Args)
	}
}

func TestCodexExperimentCommandCarriesAllKnobs(t *testing.T) {
	cmd := (codexProvider{}).ExperimentCommand(context.Background(), ExperimentSpec{
		Model:           "gpt-5.5",
		Profile:         "fast",
		Config:          map[string]string{"reasoning": "low"},
		LastMessageFile: "/tmp/last.txt",
	})
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"exec", "--json", "--output-last-message /tmp/last.txt", "--model gpt-5.5", "--profile fast", "--config reasoning=low"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex experiment command missing %q: %v", want, cmd.Args)
		}
	}
	if cmd.Args[len(cmd.Args)-1] != "-" {
		t.Fatalf("codex experiment command must read the prompt from stdin (end with '-'): %v", cmd.Args)
	}
}

func TestClaudeExperimentCommandIsHeadlessAndIgnoresCodexKnobs(t *testing.T) {
	cmd := (claudeProvider{}).ExperimentCommand(context.Background(), ExperimentSpec{
		Model:           "claude-sonnet-5",
		Profile:         "fast",
		Config:          map[string]string{"reasoning": "low"},
		LastMessageFile: "/tmp/last.txt",
	})
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-p", "--output-format stream-json", "--model claude-sonnet-5"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("claude experiment command missing %q: %v", want, cmd.Args)
		}
	}
	for _, unwanted := range []string{"--output-last-message", "--profile", "--config", "exec"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("claude experiment command must not carry codex-only knob %q: %v", unwanted, cmd.Args)
		}
	}
}

func TestClaudeOutputMonitorCapturesFinalMessage(t *testing.T) {
	writer := &claudeOutputMonitor{}
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"Implemented the feature.","usage":{"input_tokens":10,"output_tokens":5}}`,
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if got := writer.FinalMessage(); got != "Implemented the feature." {
		t.Fatalf("claude FinalMessage = %q, want %q", got, "Implemented the feature.")
	}
}

func TestCodexOutputMonitorHasNoInStreamFinalMessage(t *testing.T) {
	// codex surfaces its final message via --output-last-message, not the stream.
	if got := (&codexOutputMonitor{}).FinalMessage(); got != "" {
		t.Fatalf("codex FinalMessage = %q, want empty", got)
	}
}

func TestCodexUsageLimitDetectionHandlesSplitOutput(t *testing.T) {
	writer := &codexOutputMonitor{}

	if _, err := writer.Write([]byte("ERROR: You've hit your ")); err != nil {
		t.Fatal(err)
	}
	if writer.UsageLimited() {
		t.Fatal("usage limit should not be detected before the marker is complete")
	}

	if _, err := writer.Write([]byte("usage limit. Upgrade to Pro")); err != nil {
		t.Fatal(err)
	}
	if !writer.UsageLimited() {
		t.Fatal("expected usage limit marker to be detected across writes")
	}
}

func TestCodexOutputMonitorParsesTokenUsage(t *testing.T) {
	writer := &codexOutputMonitor{}

	if _, err := writer.Write([]byte("some output\ntokens used\n")); err != nil {
		t.Fatal(err)
	}
	if writer.TokenTotal() != 0 {
		t.Fatal("token total should wait for the line after marker")
	}

	if _, err := writer.Write([]byte("35,814\n")); err != nil {
		t.Fatal(err)
	}
	if got := writer.TokenTotal(); got != 35814 {
		t.Fatalf("token total = %d, want 35814", got)
	}
}

func TestCodexOutputMonitorParsesJSONTokenUsage(t *testing.T) {
	writer := &codexOutputMonitor{}

	lines := []string{
		`{"type":"turn.started"}`,
		`{"type":"turn.completed","usage":{"input_tokens":222218,"cached_input_tokens":148992,"output_tokens":2649,"reasoning_output_tokens":299}}`,
		// An item.completed line embeds command output that may mention
		// turn.completed as text; it must not be counted as a usage event.
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","aggregated_output":"grep turn.completed found nothing"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":118841,"cached_input_tokens":80640,"output_tokens":2464,"reasoning_output_tokens":873}}`,
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	got := writer.Breakdown()
	// Usage is summed across both turn.completed events.
	if got.Input != 341059 {
		t.Fatalf("input = %d, want 341059", got.Input)
	}
	if got.CachedInput != 229632 {
		t.Fatalf("cached input = %d, want 229632", got.CachedInput)
	}
	if got.Output != 5113 {
		t.Fatalf("output = %d, want 5113", got.Output)
	}
	if got.ReasoningOutput != 1172 {
		t.Fatalf("reasoning output = %d, want 1172", got.ReasoningOutput)
	}
	if got.Total != 346172 { // input + output summed across turns
		t.Fatalf("total = %d, want 346172", got.Total)
	}
	if writer.TokenTotal() != 346172 {
		t.Fatalf("token total = %d, want 346172", writer.TokenTotal())
	}
}

func TestClaudeOutputMonitorParsesResultUsage(t *testing.T) {
	writer := &claudeOutputMonitor{}

	lines := []string{
		`{"type":"system","subtype":"init","model":"claude-sonnet-5"}`,
		// Per-message usage should NOT be counted; only the terminal result.
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":17573,"cache_creation_input_tokens":6897,"output_tokens":7}}}`,
		`{"type":"result","subtype":"success","is_error":false,"usage":{"input_tokens":10,"cache_creation_input_tokens":6897,"cache_read_input_tokens":17573,"output_tokens":39}}`,
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	got := writer.Breakdown()
	// input = fresh(10) + cache_read(17573) + cache_creation(6897)
	if got.Input != 24480 {
		t.Fatalf("input = %d, want 24480", got.Input)
	}
	if got.CachedInput != 24470 {
		t.Fatalf("cached input = %d, want 24470", got.CachedInput)
	}
	if got.Output != 39 {
		t.Fatalf("output = %d, want 39", got.Output)
	}
	if got.Total != 24519 { // input + output
		t.Fatalf("total = %d, want 24519", got.Total)
	}
	if writer.UsageLimited() {
		t.Fatal("did not expect usage-limited on a successful result")
	}
}

func TestClaudeOutputMonitorDetectsRejectedRateLimit(t *testing.T) {
	writer := &claudeOutputMonitor{}

	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour"}}`
	if _, err := writer.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}

	if !writer.UsageLimited() {
		t.Fatal("expected rejected rate-limit event to set usage-limited")
	}
}

func TestOpenCodeCommandIgnoresMaxTurns(t *testing.T) {
	cmd := (opencodeProvider{}).Command(context.Background(), "my-model", 20)
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "run --format json --auto --model my-model") {
		t.Fatalf("unexpected opencode command: %v", cmd.Args)
	}
}

func TestOpenCodeExperimentCommand(t *testing.T) {
	cmd := (opencodeProvider{}).ExperimentCommand(context.Background(), ExperimentSpec{
		Model: "anthropic/claude-3-7-sonnet",
	})
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "run --format json --auto --model anthropic/claude-3-7-sonnet") {
		t.Fatalf("unexpected opencode experiment command: %v", cmd.Args)
	}
}

func TestOpenCodeOutputMonitorParsesStepFinishAndTokens(t *testing.T) {
	writer := &opencodeOutputMonitor{}

	lines := []string{
		`{"type":"step_start","sessionID":"ses_123"}`,
		`{"type":"step_finish","sessionID":"ses_123","part":{"tokens":{"input":100,"output":50,"reasoning":20,"cache":{"read":15,"write":5}}}}`,
		`{"type":"step_finish","sessionID":"ses_123","part":{"tokens":{"input":200,"output":30,"reasoning":10,"cache":{"read":25,"write":5}}}}`,
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	if got := writer.SessionID(); got != "ses_123" {
		t.Fatalf("sessionID = %q, want ses_123", got)
	}

	got := writer.Breakdown()
	// step 1: cached = 20, input = 120, output = 70 (50+20), total = 190
	// step 2: cached = 30, input = 230, output = 40 (30+10), total = 270
	// total Input: 350, Cached: 50, Output: 110, Reasoning: 30, Total: 460
	if got.Input != 350 {
		t.Fatalf("input = %d, want 350", got.Input)
	}
	if got.CachedInput != 50 {
		t.Fatalf("cached input = %d, want 50", got.CachedInput)
	}
	if got.Output != 110 {
		t.Fatalf("output = %d, want 110", got.Output)
	}
	if got.ReasoningOutput != 30 {
		t.Fatalf("reasoning = %d, want 30", got.ReasoningOutput)
	}
	if got.Total != 460 {
		t.Fatalf("total = %d, want 460", got.Total)
	}
}

func TestOpenCodeOutputMonitorCapturesFinalMessage(t *testing.T) {
	writer := &opencodeOutputMonitor{}

	lines := []string{
		`{"type":"text","part":{"id":"p1","text":"Hello "}}`,
		`{"type":"text","part":{"id":"p1","text":"Hello world!"}}`,
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	if got := writer.FinalMessage(); got != "Hello world!" {
		t.Fatalf("final message = %q, want 'Hello world!'", got)
	}
}

func TestOpenCodeOutputMonitorDetectsErrorsAndUsageLimit(t *testing.T) {
	writer := &opencodeOutputMonitor{}

	line := `{"type":"error","error":{"type":"provider.rate-limit","message":"Rate limit exceeded","status":429}}`
	if _, err := writer.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}

	if !writer.UsageLimited() {
		t.Fatal("expected UsageLimited to be true")
	}
	if got := writer.ErrorMessage(); got != "Rate limit exceeded" {
		t.Fatalf("errorMessage = %q, want 'Rate limit exceeded'", got)
	}
}

func TestAgyCommandIgnoresMaxTurns(t *testing.T) {
	cmd := (agyProvider{}).Command(context.Background(), "Gemini 3.8 Flash (Medium)", 20)
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--output-format stream-json --dangerously-skip-permissions --model Gemini 3.8 Flash (Medium) --print -") {
		t.Fatalf("unexpected agy command: %v", cmd.Args)
	}
}

func TestAgyExperimentCommand(t *testing.T) {
	cmd := (agyProvider{}).ExperimentCommand(context.Background(), ExperimentSpec{
		Model: "Gemini 4 Argon (High)",
	})
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--output-format stream-json --dangerously-skip-permissions --model Gemini 4 Argon (High) --print -") {
		t.Fatalf("unexpected agy experiment command: %v", cmd.Args)
	}
}

func TestAgyOutputMonitorParsesResultAndUsage(t *testing.T) {
	writer := &agyOutputMonitor{}

	lines := []string{
		`{"event":"init","conversation_id":"conv-123"}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-123","step_index":0,"state":"DONE","step_type":"user_input"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-123","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"Done!\n","usage":{"input_tokens":500,"output_tokens":50,"thinking_tokens":20,"cache_read_tokens":100,"total_tokens":550}}}`,
		`{"event":"result","result":{"conversation_id":"conv-123","status":"SUCCESS","response":"Done!\n","usage":{"input_tokens":500,"output_tokens":50,"thinking_tokens":20,"cache_read_tokens":100,"total_tokens":550}}}`,
	}
	for _, line := range lines {
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	if got := writer.SessionID(); got != "conv-123" {
		t.Fatalf("sessionID = %q, want conv-123", got)
	}
	if got := writer.FinalMessage(); got != "Done!" {
		t.Fatalf("final message = %q, want 'Done!'", got)
	}

	got := writer.Breakdown()
	if got.Input != 500 {
		t.Fatalf("input = %d, want 500", got.Input)
	}
	if got.CachedInput != 100 {
		t.Fatalf("cached input = %d, want 100", got.CachedInput)
	}
	if got.Output != 50 {
		t.Fatalf("output = %d, want 50", got.Output)
	}
	if got.ReasoningOutput != 20 {
		t.Fatalf("reasoning output = %d, want 20", got.ReasoningOutput)
	}
	if got.Total != 550 {
		t.Fatalf("total = %d, want 550", got.Total)
	}
	if writer.UsageLimited() {
		t.Fatal("expected UsageLimited to be false on success")
	}
}

func TestAgyOutputMonitorDetectsErrorsAndUsageLimit(t *testing.T) {
	writer := &agyOutputMonitor{}

	line := `{"event":"result","result":{"conversation_id":"conv-456","status":"ERROR","error":"Resource exhausted: quota exceeded (429)"}}`
	if _, err := writer.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}

	if !writer.UsageLimited() {
		t.Fatal("expected UsageLimited to be true for quota exceeded")
	}
	if got := writer.ErrorMessage(); !strings.Contains(got, "quota exceeded") {
		t.Fatalf("errorMessage = %q, expected quota exceeded", got)
	}
}
