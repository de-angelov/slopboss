package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/de-angelov/slopboss/internal/config"
)

type boardChange struct {
	File   string `json:"file"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type boardTransaction struct {
	Changes []boardChange `json:"changes"`
}

func lockBoard() (func(), error) {
	dir := config.WorkspacesRoot
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "board.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	unlock := func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
	if err := recoverBoardTransaction(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

func journalPath() string { return filepath.Join(config.WorkspacesRoot, "board-transaction.json") }

// commitBoardChanges persists intent before touching any board file. Recovery is
// idempotent and refuses to overwrite edits that do not match either snapshot.
func commitBoardChanges(changes []boardChange) error {
	data, err := json.Marshal(boardTransaction{Changes: changes})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(config.WorkspacesRoot, ".board-transaction-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, journalPath()); err != nil {
		return err
	}
	if err := syncDirectory(config.WorkspacesRoot); err != nil {
		return err
	}
	return recoverBoardTransaction()
}

func recoverBoardTransaction() error {
	data, err := os.ReadFile(journalPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var tx boardTransaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return fmt.Errorf("invalid board transaction: %w", err)
	}
	if len(tx.Changes) == 0 {
		return fmt.Errorf("empty board transaction")
	}
	paths := make([]string, len(tx.Changes))
	seen := map[string]bool{}
	for i, change := range tx.Changes {
		path, err := boardPath(change.File)
		if err != nil || seen[path] {
			return fmt.Errorf("invalid board transaction file %q", change.File)
		}
		seen[path] = true
		paths[i] = path
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(current) != change.Before && string(current) != change.After {
			return fmt.Errorf("board transaction conflict in %s; inspect %s", path, journalPath())
		}
	}
	for i, change := range tx.Changes {
		if err := writeAtomic(paths[i], change.After); err != nil {
			return fmt.Errorf("finish board transaction: %w", err)
		}
	}
	if err := os.Remove(journalPath()); err != nil {
		return err
	}
	return syncDirectory(config.WorkspacesRoot)
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
