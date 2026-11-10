package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGetLogsBoundsJournalctlWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := syslogTimeout
	syslogTimeout = 25 * time.Millisecond
	t.Cleanup(func() { syslogTimeout = previous })
	started := time.Now()
	lines := (&ServerService{}).GetLogs("10", "warning", "true")
	if !strings.Contains(strings.Join(lines, "\n"), "timed out") {
		t.Fatalf("timeout not reported: %v", lines)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("request waited for the stalled journalctl")
	}
}
