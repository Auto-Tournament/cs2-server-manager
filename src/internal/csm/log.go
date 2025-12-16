package csm

import (
	"os"
	"path/filepath"
	"strings"
)

// logDir returns the base directory where CSM writes its log files. It can be
// overridden with CSM_LOG_DIR; by default we use /var/log/csm.
func logDir() string {
	if d := os.Getenv("CSM_LOG_DIR"); d != "" {
		return d
	}
	return "/var/log/csm"
}

// AppendLog appends content to the named log file under the CSM log
// directory. Errors are ignored so that logging never breaks primary flows.
func AppendLog(filename, content string) {
	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return
	}
	if !strings.HasSuffix(content, "\n") {
		_, _ = f.WriteString("\n")
	}
}


