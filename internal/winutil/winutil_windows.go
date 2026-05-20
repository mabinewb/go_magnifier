//go:build windows

package winutil

import (
	"fmt"
	"syscall"

	"github.com/lxn/win"
)

const wdaExcludeFromCapture = 0x11
const wdaNone = 0x0

const lwaAlpha = 0x2

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procSetLayeredWindowAttrs    = user32.NewProc("SetLayeredWindowAttributes")
)

func ApplyBorderless(hwnd win.HWND) {
	applyBorderless(hwnd, false)
}

func ApplyResizableBorderless(hwnd win.HWND) {
	applyBorderless(hwnd, true)
}

func applyBorderless(hwnd win.HWND, resizable bool) {
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style &^= win.WS_CAPTION | win.WS_SYSMENU | win.WS_MINIMIZEBOX | win.WS_MAXIMIZEBOX
	if resizable {
		style |= win.WS_THICKFRAME
	} else {
		style &^= win.WS_THICKFRAME
	}
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style))

	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	exStyle |= win.WS_EX_LAYERED | win.WS_EX_TOOLWINDOW
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle))

	win.SetWindowPos(
		hwnd,
		win.HWND_TOPMOST,
		0,
		0,
		0,
		0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_FRAMECHANGED|win.SWP_SHOWWINDOW,
	)
}

func SetOpacity(hwnd win.HWND, opacity int) {
	if opacity < 0 {
		opacity = 0
	}
	if opacity > 100 {
		opacity = 100
	}
	alpha := byte(opacity * 255 / 100)
	procSetLayeredWindowAttrs.Call(uintptr(hwnd), 0, uintptr(alpha), uintptr(lwaAlpha))
}

func SetClickThrough(hwnd win.HWND, enabled bool) {
	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	if enabled {
		exStyle |= win.WS_EX_TRANSPARENT
	} else {
		exStyle &^= win.WS_EX_TRANSPARENT
	}
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle))
	win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_FRAMECHANGED)
}

func SetAlwaysOnTop(hwnd win.HWND, enabled bool) {
	insertAfter := win.HWND_NOTOPMOST
	if enabled {
		insertAfter = win.HWND_TOPMOST
	}
	win.SetWindowPos(hwnd, insertAfter, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}

func ExcludeFromCapture(hwnd win.HWND) {
	_ = SetExcludedFromCapture(hwnd, true)
}

func SetExcludedFromCapture(hwnd win.HWND, excluded bool) error {
	affinity := uintptr(wdaNone)
	if excluded {
		affinity = uintptr(wdaExcludeFromCapture)
	}
	result, _, callErr := procSetWindowDisplayAffinity.Call(uintptr(hwnd), affinity)
	if result != 0 {
		return nil
	}
	if callErr == syscall.Errno(0) {
		callErr = fmt.Errorf("unknown SetWindowDisplayAffinity failure")
	}
	return fmt.Errorf("SetWindowDisplayAffinity failed: %w", callErr)
}