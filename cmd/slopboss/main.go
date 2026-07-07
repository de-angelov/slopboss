// Command slopboss orchestrates a board-driven, multi-agent development workflow.
// It wires the cobra CLI to the internal packages: the reconcile loop
// (orchestrator + tui), backlog grooming, workspace setup, and A/B experiments.
package main

func main() {
	Execute()
}
