package experiment

import (
	"fmt"
	"strconv"
	"strings"
)

// ExperimentFileName is the conventional name of the Markdown experiment spec the
// interactive `experiment groom` session authors at the repo root.
const ExperimentFileName = "EXPERIMENT.md"

// MarkdownFormatSpec documents the Markdown experiment format. It is embedded in
// the grooming prompt (so the Team Lead writes a valid file) and shown in docs.
const MarkdownFormatSpec = `# Experiment: <short-name>

Free-form prose describing the experiment is allowed anywhere and ignored by the
parser. Structured settings are ` + "`- Key: Value`" + ` bullets.

- Task: <exact backlog task title>      (use Task OR Ticket, not both)
- Ticket: <relative/path/to/ticket.md>
- Prompt mode: bounded                  (bounded | orchestrator-dev)
- Prompt role: Dev Agent 1              (orchestrator-dev only)
- Provider: codex                       (default backend for variants)
- Timeout minutes: 90
- Prepare command: npm ci               (repeatable; omit to auto-detect)
- Skip prepare: false

## Variants

### <variant-name>
- Provider: claude                      (overrides the default; codex | claude)
- Model: claude-sonnet-5
- Profile: <codex profile>              (codex only)
- Prompt file: experiments/prompts/x.md
- Config: key=value, key2=value2        (codex only)
`

// parseMarkdownConfig parses the Markdown experiment format into an
// ExperimentConfig. Structured settings are "- Key: Value" bullets; the H1 sets
// the name; each "### Name" under "## Variants" starts a variant. Any non-bullet
// line (prose, blank, other headings) is ignored, while an unrecognized bullet
// key is an error so typos surface instead of being silently dropped.
func parseMarkdownConfig(data string) (ExperimentConfig, error) {
	var config ExperimentConfig
	var current *ExperimentVariant
	inVariants := false

	for lineNo, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue

		case strings.HasPrefix(line, "### "):
			if !inVariants {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			config.Variants = append(config.Variants, ExperimentVariant{Name: name})
			current = &config.Variants[len(config.Variants)-1]

		case strings.HasPrefix(line, "## "):
			inVariants = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "## ")), "Variants")
			current = nil

		case strings.HasPrefix(line, "# "):
			if config.Name == "" {
				config.Name = experimentNameFromHeading(strings.TrimPrefix(line, "# "))
			}

		case strings.HasPrefix(line, "- "):
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
			if !ok {
				return ExperimentConfig{}, fmt.Errorf("line %d: %q is not a 'Key: Value' setting", lineNo+1, line)
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if inVariants && current != nil {
				if err := applyVariantField(current, key, value); err != nil {
					return ExperimentConfig{}, fmt.Errorf("line %d: %w", lineNo+1, err)
				}
			} else if err := applyTopField(&config, key, value); err != nil {
				return ExperimentConfig{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
		}
	}

	return config, nil
}

func experimentNameFromHeading(heading string) string {
	heading = strings.TrimSpace(heading)
	if k, v, ok := strings.Cut(heading, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "experiment") {
		return strings.TrimSpace(v)
	}
	return heading
}

func applyTopField(config *ExperimentConfig, key, value string) error {
	switch strings.ToLower(key) {
	case "task", "task title":
		config.TaskTitle = value
	case "ticket", "ticket file":
		config.TicketFile = value
	case "task source file":
		config.TaskSourceFile = value
	case "prompt mode":
		config.PromptMode = value
	case "prompt role":
		config.PromptRole = value
	case "prompt branch":
		config.PromptBranch = value
	case "provider":
		config.Provider = value
	case "source workspace":
		config.SourceWorkspace = value
	case "base branch":
		config.BaseBranch = value
	case "output dir":
		config.OutputDir = value
	case "timeout minutes":
		n, err := parseIntField(key, value)
		if err != nil {
			return err
		}
		config.TimeoutMinutes = n
	case "prepare timeout minutes":
		n, err := parseIntField(key, value)
		if err != nil {
			return err
		}
		config.PrepareTimeoutMinutes = n
	case "prepare command":
		config.PrepareCommands = append(config.PrepareCommands, value)
	case "skip prepare":
		b, err := parseBoolField(key, value)
		if err != nil {
			return err
		}
		config.SkipPrepare = b
	default:
		return fmt.Errorf("unknown experiment setting %q", key)
	}
	return nil
}

func applyVariantField(variant *ExperimentVariant, key, value string) error {
	switch strings.ToLower(key) {
	case "provider":
		variant.Provider = value
	case "model":
		variant.Model = value
	case "profile":
		variant.Profile = value
	case "prompt file":
		variant.PromptFile = value
	case "config":
		m, err := parseConfigMap(value)
		if err != nil {
			return err
		}
		variant.Config = m
	default:
		return fmt.Errorf("unknown variant setting %q", key)
	}
	return nil
}

func parseIntField(key, value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a number, got %q", key, value)
	}
	return n, nil
}

func parseBoolField(key, value string) (bool, error) {
	b, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q", key, value)
	}
	return b, nil
}

func parseConfigMap(value string) (map[string]string, error) {
	m := map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("config entry %q must be key=value", pair)
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m, nil
}
