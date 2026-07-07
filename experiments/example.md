# Experiment: codex-vs-claude

Example experiment spec in the Markdown format `slopboss experiment groom`
produces. Copy it to `EXPERIMENT.md` at your repo root (or pass it directly), set
`Task:` to a real backlog task title, then run:

    slopboss experiment run --config experiments/example.md --dry-run

Prose like this paragraph is ignored by the parser — only `- Key: Value` bullets,
the `# Experiment:` heading, and `### variant` sections are read.

- Task: Add dark-mode toggle
- Prompt mode: bounded
- Provider: codex
- Timeout minutes: 90

## Variants

### codex-baseline
- Provider: codex
- Model: gpt-5.5

### claude-baseline
- Provider: claude
- Model: claude-sonnet-5
