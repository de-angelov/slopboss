# Eval: codex-vs-claude

Example eval spec in the Markdown format `slopboss eval groom` produces. Copy it
to `EVAL.md` at your repo root (or pass it with `--config`), set `Task:` to a real
backlog task title, then run:

    slopboss eval run --config evals/example.md --dry-run

Prose like this paragraph is ignored by the parser — only `- Key: Value` bullets,
the `# Eval:` heading, and `### variant` sections are read.

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
