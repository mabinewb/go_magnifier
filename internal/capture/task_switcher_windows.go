//go:build windows

package capture

import (
	"strings"
	"syscall"

	"github.com/lxn/win"
)

func shouldSuspendCapture() bool {
	hwnd := win.GetForegroundWindow()
	if hwnd == 0 {
		return false
	}

	className := strings.ToLower(windowClassName(hwnd))
	switch className {
	case "multitaskingviewframe", "taskswitcherwnd", "xamlexplorerhostislandwindow":
		return true
	default:
		return false
	}
}

func windowClassName(hwnd win.HWND) string {
	buf := make([]uint16, 256)
	count, err := win.GetClassName(hwnd, &buf[0], len(buf))
	if err != nil || count == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:count])
}