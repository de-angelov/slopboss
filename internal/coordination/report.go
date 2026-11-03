// Package coordination defines the Team Lead's durable outcome contract.
package coordination

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ReportPathEnv = "SLOPBOSS_COORDINATION_REPORT"

type Report struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func (r Report) Validate() error {
	switch r.Status {
	case "acted", "idle", "blocked":
	default:
		return fmt.Errorf("status must be acted, idle, or blocked")
	}
	if strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 2000 {
		return fmt.Errorf("summary must be 1 to 2000 characters")
	}
	return nil
}

// WriteReport writes one result for the current controller-issued session.
func WriteReport(path string, report Report) error {
	if path == "" {
		return fmt.Errorf("no active Team Lead coordination session")
	}
	if err := report.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".coordination-result-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func ReadReport(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("invalid coordination result: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Report{}, err
	}
	return r, nil
}
