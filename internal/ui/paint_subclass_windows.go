//go:build windows

package ui

import (
	"fmt"
	"sync"
	"syscall"

	"github.com/lxn/win"
)

var (
	gwlpWndProc = int32(-4)

	paintSubclassMu        sync.Mutex
	paintSubclassHandlers  = map[win.HWND]func(){}
	paintSubclassMessages  = map[win.HWND]func(win.HWND, uint32, uintptr, uintptr) (bool, uintptr){}
	paintSubclassOldProc   = map[win.HWND]uintptr{}

	paintUser32            = syscall.NewLazyDLL("user32.dll")
	procSetWindowLongPtrW  = paintUser32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW    = paintUser32.NewProc("CallWindowProcW")
	paintSubclassWndProcCB = syscall.NewCallback(paintSubclassWndProc)
)

func installPaintSubclass(hwnd win.HWND, onPaint func(), onMessage func(win.HWND, uint32, uintptr, uintptr) (bool, uintptr)) error {
	paintSubclassMu.Lock()
	defer paintSubclassMu.Unlock()

	if _, exists := paintSubclassOldProc[hwnd]; exists {
		paintSubclassHandlers[hwnd] = onPaint
		paintSubclassMessages[hwnd] = onMessage
		return nil
	}

	oldProc, _, callErr := procSetWindowLongPtrW.Call(
		uintptr(hwnd),
		uintptr(gwlpWndProc),
		paintSubclassWndProcCB,
	)
	if oldProc == 0 && callErr != syscall.Errno(0) {
		return fmt.Errorf("install paint subclass: %w", callErr)
	}

	paintSubclassOldProc[hwnd] = oldProc
	paintSubclassHandlers[hwnd] = onPaint
	paintSubclassMessages[hwnd] = onMessage
	return nil
}

func paintSubclassWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	handle := win.HWND(hwnd)

	paintSubclassMu.Lock()
	oldProc := paintSubclassOldProc[handle]
	onPaint := paintSubclassHandlers[handle]
	onMessage := paintSubclassMessages[handle]
	paintSubclassMu.Unlock()

	if oldProc == 0 {
		return 0
	}

	if onMessage != nil {
		if handled, result := onMessage(handle, msg, wParam, lParam); handled {
			return result
		}
	}

	switch msg {
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_PAINT:
		result, _, _ := procCallWindowProcW.Call(oldProc, hwnd, uintptr(msg), wParam, lParam)
		if onPaint != nil {
			onPaint()
		}
		return result
	case win.WM_NCDESTROY:
		result, _, _ := procCallWindowProcW.Call(oldProc, hwnd, uintptr(msg), wParam, lParam)
		paintSubclassMu.Lock()
		delete(paintSubclassHandlers, handle)
		delete(paintSubclassMessages, handle)
		delete(paintSubclassOldProc, handle)
		paintSubclassMu.Unlock()
		return result
	default:
		result, _, _ := procCallWindowProcW.Call(oldProc, hwnd, uintptr(msg), wParam, lParam)
		return result
	}
}