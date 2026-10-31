//go:build windows

package board

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/de-angelov/slopboss/internal/config"
)

func withBoardLock(fn func() error) error {
	file, err := os.OpenFile(filepath.Join(config.RepoRoot, ".slopboss-board.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	var overlapped windows.Overlapped
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return err
	}
	defer windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
	return fn()
}
