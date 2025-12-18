package csm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logDir returns the base directory where CSM writes its log files. It can be
// overridden with CSM_LOG_DIR; by default we use the directory containing the
// csm binary so logs are colocated with the manager rather than the caller's
// current working directory. On servers you can set CSM_LOG_DIR to e.g.
// /var/log/csm to centralise logs.
func logDir() string {
	if d := os.Getenv("CSM_LOG_DIR"); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		if dir := filepath.Dir(exe); dir != "" {
			return dir
		}
	}
	return "."
}

// AppendLog appends content to the single consolidated log file (csm.log)
// under the CSM log directory. The filename argument is accepted for
// backwards compatibility but ignored so that all logs end up in one place.
// Errors are ignored so that logging never breaks primary flows.
func AppendLog(filename, content string) {
	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "csm.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// Preserve backwards compatibility (all logs in csm.log) but annotate the
	// logical filename so different flows remain distinguishable in a single
	// consolidated log.
	contentToWrite := content
	if strings.TrimSpace(content) != "" && filename != "" && filename != "csm.log" {
		prefix := fmt.Sprintf("[log=%s]\n", filename)
		contentToWrite = prefix + content
	}

	if _, err := f.WriteString(contentToWrite); err != nil {
		_ = f.Close()
		return
	}
	if !strings.HasSuffix(contentToWrite, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			_ = f.Close()
			return
		}
	}
	_ = f.Close()
}

// LogAction writes a structured log entry for a high-level action (TUI or CLI).
// Errors from logging are ignored so they never interfere with primary flows.
func LogAction(source, action, output string, err error) {
	ts := time.Now().Format(time.RFC3339)

	var b strings.Builder
	fmt.Fprintf(&b, "[%s] [%s] Action: %s\n", ts, source, action)
	if err != nil {
		fmt.Fprintf(&b, "Error: %v\n", err)
	}
	if strings.TrimSpace(output) != "" {
		b.WriteString("Output:\n")
		b.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("----\n")

	AppendLog("csm.log", b.String())
}
