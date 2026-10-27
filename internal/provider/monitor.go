package provider

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

const codexUsageLimitMarker = "You've hit your usage limit"

// TokenBreakdown is the per-session token accounting shared by every backend
// monitor. Total is what the UI and cumulative counters use; the split fields
// feed the orchestrator's breakdown log so a large total can be attributed to
// cheap cached input versus fresh input/output/reasoning.
type TokenBreakdown struct {
	Input           int
	CachedInput     int
	Output          int
	ReasoningOutput int
	Total           int
}

// parseFlexibleInt parses an integer that may contain thousands separators. It
// is duplicated (small and pure) from the experiment package so the monitor has
// no dependency on the experiment harness.
func parseFlexibleInt(value string) int {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	parsed, _ := strconv.Atoi(value)
	return parsed
}

// ---------------------------------------------------------------------------
// Codex
// ---------------------------------------------------------------------------

type codexOutputMonitor struct {
	mu                   sync.Mutex
	tail                 string
	line                 string
	nextLineIsTokenTotal bool
	usageLimitDetected   bool
	sessionTokenTotal    int
	jsonUsage            TokenBreakdown
	haveJSONUsage        bool
}

func (w *codexOutputMonitor) Write(p []byte) (int, error) {
	text := string(p)

	w.mu.Lock()
	w.detectUsageLimitLocked(text)
	w.detectTokenUsageLocked(text)
	w.mu.Unlock()

	return len(p), nil
}

func (w *codexOutputMonitor) detectUsageLimitLocked(text string) {
	combined := w.tail + text
	if strings.Contains(combined, codexUsageLimitMarker) {
		w.usageLimitDetected = true
	}
	if len(combined) > len(codexUsageLimitMarker) {
		w.tail = combined[len(combined)-len(codexUsageLimitMarker):]
	} else {
		w.tail = combined
	}
}

func (w *codexOutputMonitor) detectTokenUsageLocked(text string) {
	for _, r := range text {
		if r == '\n' {
			w.consumeCodexOutputLineLocked(w.line)
			w.line = ""
			continue
		}
		if r != '\r' {
			w.line += string(r)
		}
	}
}

func (w *codexOutputMonitor) consumeCodexOutputLineLocked(line string) {
	trimmed := strings.TrimSpace(line)

	// Prefer the structured token_count events emitted under --json. Any line
	// that parses as a codex JSON event is handled here; the plaintext scrape
	// below is kept only as a fallback for non-json output.
	if strings.HasPrefix(trimmed, "{") && w.consumeCodexJSONLineLocked(trimmed) {
		return
	}

	if w.nextLineIsTokenTotal {
		if tokens := parseFlexibleInt(trimmed); tokens > 0 {
			w.sessionTokenTotal += tokens
		}
		w.nextLineIsTokenTotal = false
		return
	}
	if strings.EqualFold(trimmed, "tokens used") {
		w.nextLineIsTokenTotal = true
	}
}

// consumeCodexJSONLineLocked parses one codex `exec --json` event line. Token
// accounting arrives as per-turn `turn.completed` events, e.g.
//
//	{"type":"turn.completed","usage":{"input_tokens":222218,"cached_input_tokens":148992,"output_tokens":2649,"reasoning_output_tokens":299}}
//
// A session spans multiple turns, so we sum usage across every turn.completed
// event rather than taking the latest. Returns true when the line was a
// turn.completed usage event we consumed. The cheap substring pre-check keeps us
// from JSON-parsing the large item.completed lines (which embed full command
// output) on every write.
func (w *codexOutputMonitor) consumeCodexJSONLineLocked(line string) bool {
	if !strings.Contains(line, `"turn.completed"`) {
		return false
	}

	var event struct {
		Type  string `json:"type"`
		Usage *struct {
			InputTokens           int `json:"input_tokens"`
			CachedInputTokens     int `json:"cached_input_tokens"`
			OutputTokens          int `json:"output_tokens"`
			ReasoningOutputTokens int `json:"reasoning_output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return false
	}

	if event.Type != "turn.completed" || event.Usage == nil {
		return false
	}

	u := event.Usage
	w.jsonUsage.Input += u.InputTokens
	w.jsonUsage.CachedInput += u.CachedInputTokens
	w.jsonUsage.Output += u.OutputTokens
	w.jsonUsage.ReasoningOutput += u.ReasoningOutputTokens
	// turn.completed carries no total field; input_tokens already includes cached
	// input and output_tokens already includes reasoning, so the session total is
	// simply input + output summed across turns.
	w.jsonUsage.Total += u.InputTokens + u.OutputTokens
	w.haveJSONUsage = true
	return true
}

func (w *codexOutputMonitor) tokenTotalLocked() int {
	if strings.TrimSpace(w.line) != "" {
		w.consumeCodexOutputLineLocked(w.line)
		w.line = ""
	}
	if w.haveJSONUsage {
		return w.jsonUsage.Total
	}
	return w.sessionTokenTotal
}

func (w *codexOutputMonitor) TokenTotal() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tokenTotalLocked()
}

func (w *codexOutputMonitor) Breakdown() TokenBreakdown {
	w.mu.Lock()
	defer w.mu.Unlock()

	total := w.tokenTotalLocked()
	if w.haveJSONUsage {
		return w.jsonUsage
	}
	return TokenBreakdown{Total: total}
}

func (w *codexOutputMonitor) UsageLimited() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.usageLimitDetected
}

// FinalMessage returns "" for codex: its final assistant message is captured out
// of band via the --output-last-message file the experiment harness passes, not
// from the event stream.
func (w *codexOutputMonitor) FinalMessage() string { return "" }

// ---------------------------------------------------------------------------
// Claude Code
// ---------------------------------------------------------------------------

// claudeOutputMonitor parses Claude Code's stream-json output. The terminal
// `result` event carries the session's cumulative usage. Unlike codex,
// input_tokens excludes cached tokens, which are reported separately as
// cache_read_input_tokens and cache_creation_input_tokens — so total input is
// their sum.
type claudeOutputMonitor struct {
	mu           sync.Mutex
	line         string
	usage        TokenBreakdown
	usageLimited bool
	finalMessage string
}

func (w *claudeOutputMonitor) Write(p []byte) (int, error) {
	w.mu.Lock()
	for _, r := range string(p) {
		if r == '\n' {
			w.consumeLineLocked(w.line)
			w.line = ""
			continue
		}
		if r != '\r' {
			w.line += string(r)
		}
	}
	w.mu.Unlock()

	return len(p), nil
}

func (w *claudeOutputMonitor) consumeLineLocked(line string) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return
	}

	var event struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Usage   *struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
		} `json:"usage"`
		RateLimitInfo *struct {
			Status string `json:"status"`
		} `json:"rate_limit_info"`
	}

	if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
		return
	}

	// A rejected rate-limit event is Claude's usage-limit signal.
	if event.Type == "rate_limit_event" && event.RateLimitInfo != nil &&
		strings.EqualFold(event.RateLimitInfo.Status, "rejected") {
		w.usageLimited = true
	}

	if event.Type == "result" {
		// The terminal result event carries both the cumulative usage and the
		// final assistant text (the `result` field), which experiments surface as
		// the variant's final-response summary.
		if event.Result != "" {
			w.finalMessage = event.Result
		}
		if event.Usage != nil {
			u := event.Usage
			cached := u.CacheReadInputTokens + u.CacheCreationInputTokens
			input := u.InputTokens + cached
			w.usage = TokenBreakdown{
				Input:       input,
				CachedInput: cached,
				Output:      u.OutputTokens,
				Total:       input + u.OutputTokens,
			}
		}
	}
}

func (w *claudeOutputMonitor) Breakdown() TokenBreakdown {
	w.mu.Lock()
	defer w.mu.Unlock()

	if strings.TrimSpace(w.line) != "" {
		w.consumeLineLocked(w.line)
		w.line = ""
	}
	return w.usage
}

func (w *claudeOutputMonitor) UsageLimited() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.usageLimited
}

// FinalMessage returns the final assistant text from the terminal result event.
func (w *claudeOutputMonitor) FinalMessage() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	if strings.TrimSpace(w.line) != "" {
		w.consumeLineLocked(w.line)
		w.line = ""
	}
	return w.finalMessage
}

// ---------------------------------------------------------------------------
// OpenCode
// ---------------------------------------------------------------------------

// opencodeOutputMonitor parses OpenCode's `run --format json` output. Each
// step_finish event carries that step's token usage; the final step_finish can
// be omitted, in which case the provider falls back to `opencode session export`.
// The terminal assistant text arrives as one or more `text` events.
type opencodeOutputMonitor struct {
	mu           sync.Mutex
	line         string
	sessionID    string
	usage        TokenBreakdown
	usageLimited bool
	errorMessage string
	finalMessage string
	textMessage  string
	textParts    []opencodeTextPart
}

type opencodeTextPart struct {
	id   string
	text string
}

func (w *opencodeOutputMonitor) Write(p []byte) (int, error) {
	w.mu.Lock()
	for _, r := range string(p) {
		if r == '\n' {
			w.consumeLineLocked(w.line)
			w.line = ""
			continue
		}
		if r != '\r' {
			w.line += string(r)
		}
	}
	w.mu.Unlock()

	return len(p), nil
}

func (w *opencodeOutputMonitor) consumeLineLocked(line string) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return
	}

	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Part      struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
			Type      string `json:"type"`
			Text      string `json:"text"`
			Tokens    struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"part"`
		Error struct {
			Type       string `json:"type"`
			Message    string `json:"message"`
			Status     int    `json:"status"`
			RetryAfter int    `json:"retryAfterMs"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
		return
	}

	if event.SessionID != "" {
		w.sessionID = event.SessionID
	} else if event.Part.SessionID != "" {
		w.sessionID = event.Part.SessionID
	}

	switch event.Type {
	case "text":
		part := event.Part
		if part.Text == "" {
			break
		}
		if part.MessageID != "" && part.MessageID != w.textMessage {
			w.textMessage = part.MessageID
			w.textParts = nil
		}
		if part.ID != "" {
			updated := false
			for i := range w.textParts {
				if w.textParts[i].id == part.ID {
					w.textParts[i].text = part.Text
					updated = true
					break
				}
			}
			if !updated {
				w.textParts = append(w.textParts, opencodeTextPart{id: part.ID, text: part.Text})
			}
		} else {
			w.textParts = append(w.textParts, opencodeTextPart{text: part.Text})
		}

		var b strings.Builder
		for i, tp := range w.textParts {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(tp.text)
		}
		w.finalMessage = b.String()
	case "step_finish":
		t := event.Part.Tokens
		cached := t.Cache.Read + t.Cache.Write
		input := t.Input + cached
		output := t.Output + t.Reasoning
		w.usage.Input += input
		w.usage.CachedInput += cached
		w.usage.Output += output
		w.usage.ReasoningOutput += t.Reasoning
		w.usage.Total += input + output
	case "error":
		msg := event.Error.Message
		if msg == "" {
			msg = event.Error.Type
		}
		if msg != "" {
			w.errorMessage = msg
		}
		if isOpenCodeUsageLimited(event.Error.Type, event.Error.Status) {
			w.usageLimited = true
		}
	}
}

func (w *opencodeOutputMonitor) Breakdown() TokenBreakdown {
	w.mu.Lock()
	defer w.mu.Unlock()

	if strings.TrimSpace(w.line) != "" {
		w.consumeLineLocked(w.line)
		w.line = ""
	}
	return w.usage
}

func (w *opencodeOutputMonitor) TokenTotal() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	if strings.TrimSpace(w.line) != "" {
		w.consumeLineLocked(w.line)
		w.line = ""
	}
	return w.usage.Total
}

func (w *opencodeOutputMonitor) UsageLimited() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.usageLimited
}

func (w *opencodeOutputMonitor) FinalMessage() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	if strings.TrimSpace(w.line) != "" {
		w.consumeLineLocked(w.line)
		w.line = ""
	}
	return w.finalMessage
}

func (w *opencodeOutputMonitor) SessionID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sessionID
}

func (w *opencodeOutputMonitor) ErrorMessage() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.errorMessage
}

func isOpenCodeUsageLimited(errorType string, status int) bool {
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "provider.rate-limit", "provider.quota", "rate-limit", "quota", "rate_limit":
		return true
	}
	if status == 429 {
		return true
	}
	lower := strings.ToLower(errorType)
	return strings.Contains(lower, "rate") && strings.Contains(lower, "limit") ||
		strings.Contains(lower, "quota")
}
