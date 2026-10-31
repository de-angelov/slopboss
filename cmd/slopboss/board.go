package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/board"
)

var boardCmd = &cobra.Command{
	Use:         "board",
	Short:       "Read and update board task metadata structurally",
	Annotations: needsBoard,
}

var boardGetCmd = &cobra.Command{
	Use:   "get [TASK-ID]",
	Short: "Print one task, or all live tasks, as JSON",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var value any
		var err error
		if len(args) == 1 {
			value, err = board.Get(args[0])
		} else {
			value, err = board.ReadBoardTasks()
		}
		if err != nil {
			return err
		}
		return writeJSON(value)
	},
}

var (
	createID, createTitle, createCategory, createBranch, createDependencies, createBody string
)

var boardCreateCmd = &cobra.Command{
	Use:   "create TASK-ID",
	Short: "Create a validated AFK or HITL task in the backlog",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		task, err := board.CreateBacklogTask(board.Task{ID: args[0], Title: createTitle, Category: createCategory, Branch: createBranch, Dependencies: createDependencies, Body: createBody})
		if err != nil {
			return err
		}
		return writeJSON(task)
	},
}

var (
	setOwner, setBranch, setStatus, setCategory, setDependencies, setBlockedBy, setNote string
	setClearBlockedBy                                                                   bool
	setFile, setSection                                                                 string
)

var boardSetCmd = &cobra.Command{
	Use:   "set TASK-ID",
	Short: "Validate and update a task's metadata or location",
	Long: `Update machine-owned task metadata without editing Markdown by hand.

To move a task, pass both --file (backlog, tasks, or archive) and --section.
The task body is preserved.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		u := board.Update{Owner: setOwner, Branch: setBranch, Status: setStatus, Category: setCategory, Dependencies: setDependencies, BlockedBy: setBlockedBy, Note: setNote, ClearBlockedBy: setClearBlockedBy, File: setFile, Section: setSection}
		if noBoardChanges(u) {
			return fmt.Errorf("supply at least one field to update")
		}
		task, err := board.Set(args[0], u)
		if err != nil {
			return err
		}
		return writeJSON(task)
	},
}

func noBoardChanges(u board.Update) bool {
	return strings.TrimSpace(u.Owner) == "" && strings.TrimSpace(u.Branch) == "" && strings.TrimSpace(u.Status) == "" && strings.TrimSpace(u.Category) == "" && strings.TrimSpace(u.Dependencies) == "" && strings.TrimSpace(u.BlockedBy) == "" && strings.TrimSpace(u.Note) == "" && !u.ClearBlockedBy && strings.TrimSpace(u.File) == "" && strings.TrimSpace(u.Section) == ""
}

func writeJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}

func init() {
	boardSetCmd.Flags().StringVar(&setOwner, "owner", "", "task owner")
	boardSetCmd.Flags().StringVar(&setBranch, "branch", "", "task branch")
	boardSetCmd.Flags().StringVar(&setStatus, "status", "", "Backlog, In Progress, Blocked, or Done")
	boardSetCmd.Flags().StringVar(&setCategory, "category", "", "AFK or HITL")
	boardSetCmd.Flags().StringVar(&setDependencies, "dependencies", "", "dependency list")
	boardSetCmd.Flags().StringVar(&setBlockedBy, "blocked-by", "", "current blocker")
	boardSetCmd.Flags().StringVar(&setNote, "note", "", "append a progress or blocker note to the task body")
	boardSetCmd.Flags().BoolVar(&setClearBlockedBy, "clear-blocked-by", false, "remove the Blocked by field")
	boardSetCmd.Flags().StringVar(&setFile, "file", "", "destination: backlog, tasks, or archive")
	boardSetCmd.Flags().StringVar(&setSection, "section", "", "destination section; requires --file")
	boardCreateCmd.Flags().StringVar(&createTitle, "title", "", "task title")
	boardCreateCmd.Flags().StringVar(&createCategory, "category", "AFK", "AFK or HITL")
	boardCreateCmd.Flags().StringVar(&createBranch, "branch", "none", "proposed task branch")
	boardCreateCmd.Flags().StringVar(&createDependencies, "dependencies", "none", "dependency task IDs")
	boardCreateCmd.Flags().StringVar(&createBody, "body", "", "task body with Objective, Scope, Out of Scope, Acceptance Criteria, Verification")
	boardCmd.AddCommand(boardGetCmd, boardSetCmd, boardCreateCmd)
	rootCmd.AddCommand(boardCmd)
}
