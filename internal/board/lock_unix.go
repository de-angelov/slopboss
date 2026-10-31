//go:build !windows

package board

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/de-angelov/slopboss/internal/config"
)

func withBoardLock(fn func() error) error {
	file, err := os.OpenFile(filepath.Join(config.RepoRoot, ".slopboss-board.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return fn()
}
