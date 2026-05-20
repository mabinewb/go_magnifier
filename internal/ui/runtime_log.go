package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var runtimeLogMu sync.Mutex

func runtimeLogPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		return ""
	}
	return filepath.Join(configDir, appName, "runtime.log")
}

func appendRuntimeLog(level string, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	path := runtimeLogPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()

	_, _ = fmt.Fprintf(file, "%s [%s] %s\r\n", time.Now().Format(time.RFC3339), normalizeRuntimeLogLevel(level), message)
}

func normalizeRuntimeLogLevel(level string) string {
	level = strings.ToUpper(strings.TrimSpace(level))
	if level == "" {
		return "INFO"
	}
	return level
}

func runtimeLogLevelForStatus(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "INFO"
	}
	if strings.Contains(message, "오류") || strings.Contains(message, "실패") || strings.Contains(message, "panic") || strings.Contains(strings.ToLower(message), "error") {
		return "ERROR"
	}
	if strings.Contains(message, "경고") || strings.Contains(message, "warning") {
		return "WARN"
	}
	return "INFO"
}