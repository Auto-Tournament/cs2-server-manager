package csm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LogLevel represents the logging level
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

var (
	// logLevel controls the minimum log level (default: INFO)
	logLevel LogLevel = LogLevelInfo
	
	// logOutput is where structured logs are written (default: stderr)
	logOutput io.Writer = os.Stderr
	
	// logJSON controls whether logs are in JSON format
	logJSON bool
)

func init() {
	// Check for CSM_LOG_LEVEL environment variable
	if envLevel := os.Getenv("CSM_LOG_LEVEL"); envLevel != "" {
		switch strings.ToUpper(envLevel) {
		case "DEBUG":
			logLevel = LogLevelDebug
		case "INFO":
			logLevel = LogLevelInfo
		case "WARN", "WARNING":
			logLevel = LogLevelWarn
		case "ERROR":
			logLevel = LogLevelError
		}
	}
	
	// Check for JSON logging
	if os.Getenv("CSM_LOG_JSON") != "" {
		logJSON = true
	}
}

// formatLog formats a log entry with level, timestamp, and message
func formatLog(level string, msg string, args ...any) string {
	ts := time.Now().Format(time.RFC3339)
	
	if logJSON {
		// Simple JSON format
		var parts []string
		parts = append(parts, fmt.Sprintf(`"time":"%s"`, ts))
		parts = append(parts, fmt.Sprintf(`"level":"%s"`, level))
		parts = append(parts, fmt.Sprintf(`"msg":%q`, msg))
		
		// Add key-value pairs
		for i := 0; i < len(args); i += 2 {
			if i+1 < len(args) {
				key := fmt.Sprintf("%v", args[i])
				val := fmt.Sprintf("%v", args[i+1])
				parts = append(parts, fmt.Sprintf(`%q:%q`, key, val))
			}
		}
		
		return "{" + strings.Join(parts, ",") + "}\n"
	}
	
	// Text format
	var parts []string
	parts = append(parts, fmt.Sprintf("[%s]", ts))
	parts = append(parts, fmt.Sprintf("[%s]", level))
	parts = append(parts, msg)
	
	// Add key-value pairs
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			parts = append(parts, fmt.Sprintf("%v=%v", args[i], args[i+1]))
		}
	}
	
	return strings.Join(parts, " ") + "\n"
}

// LogDebug logs a debug message
func LogDebug(msg string, args ...any) {
	if logLevel <= LogLevelDebug {
		fmt.Fprint(logOutput, formatLog("DEBUG", msg, args...))
	}
}

// LogInfo logs an info message
func LogInfo(msg string, args ...any) {
	if logLevel <= LogLevelInfo {
		fmt.Fprint(logOutput, formatLog("INFO", msg, args...))
	}
}

// LogWarn logs a warning message
func LogWarn(msg string, args ...any) {
	if logLevel <= LogLevelWarn {
		fmt.Fprint(logOutput, formatLog("WARN", msg, args...))
	}
}

// LogError logs an error message
func LogError(msg string, err error, args ...any) {
	if logLevel <= LogLevelError {
		allArgs := append([]any{"error", err}, args...)
		fmt.Fprint(logOutput, formatLog("ERROR", msg, allArgs...))
	}
}

// logDir returns the base directory where CSM writes its log files. It can be
// overridden with CSM_LOG_DIR; by default we use a logs/ subdirectory under
// the resolved CSM root so logs live alongside overrides/ and game_files/
// rather than in the caller's current working directory.
func logDir() string {
	if d := os.Getenv("CSM_LOG_DIR"); d != "" {
		return d
	}
	root := ResolveRoot()
	return filepath.Join(root, "logs")
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
	// Use structured logging
	if err != nil {
		LogError("Action failed", err, "source", source, "action", action)
	} else {
		LogInfo("Action completed", "source", source, "action", action)
	}
	
	// Also write to the consolidated log file for backwards compatibility
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
