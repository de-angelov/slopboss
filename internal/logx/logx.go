// Package logx is slopboss's tiny append-only event log. Every subsystem writes
// human-readable lines to the shared run log file (config.LogFilePath); the TUI
// tails it for its footer. It depends only on config.
package logx

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
)

var (
	mu          sync.Mutex
	ansiColorRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func openOutput() (*os.File, error) {
	return os.OpenFile(config.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

// Writer is an io.Writer that appends to the shared log under a mutex. It is
// wired to a backend session's cmd.Stdout/Stderr, so a failure to open or write
// the log must never fail the caller (which would break the backend process's
// output pipe). The run command creates LogsRoot up front; if it is somehow
// missing we simply drop the log line.
type Writer struct{}

func (Writer) Write(p []byte) (int, error) {
	mu.Lock()
	defer mu.Unlock()

	if f, err := openOutput(); err == nil {
		_, _ = f.Write(p)
		_ = f.Close()
	}
	return len(p), nil
}

// Event appends a single timestamped, formatted line to the log.
func Event(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()

	line := fmt.Sprintf("%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))

	f, err := openOutput()
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.WriteString(line)
}

// LatestLogLine returns the last non-empty line of the log (ANSI stripped), or a
// placeholder when the log is missing or empty.
func LatestLogLine() string {
	f, err := os.Open(config.LogFilePath)
	if err != nil {
		return "[no log]"
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return "[no log]"
	}

	const maxRead int64 = 4096

	size := info.Size()
	start := size - maxRead
	if start < 0 {
		start = 0
	}

	buf := make([]byte, size-start)
	_, err = f.ReadAt(buf, start)
	if err != nil && err != io.EOF {
		return "[no log]"
	}

	text := strings.TrimSpace(string(buf))
	if text == "" {
		return "[no log]"
	}

	lines := strings.Split(text, "\n")
	last := ansiColorRe.ReplaceAllString(lines[len(lines)-1], "")

	return last
}
