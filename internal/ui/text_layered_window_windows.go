//go:build windows

package ui

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/lxn/win"

	"gomagnifier/internal/capture"
	"gomagnifier/internal/model"
	"gomagnifier/internal/winutil"
)

var (
	textLayerClassOnce sync.Once
	textLayerClassErr  error
	textLayerClassName = syscall.StringToUTF16Ptr("GoMagnifierTextLayeredWindow")
	textLayerWndProcCB = syscall.NewCallback(textLayeredWndProc)
)

type textLayeredWindow struct {
	hwnd         win.HWND
	dibDC        win.HDC
	dibBitmap    win.HBITMAP
	dibOldBitmap win.HGDIOBJ
	dibBits      unsafe.Pointer
	frameWidth   int
	frameHeight  int
}

func newTextLayeredWindow(bounds model.Rect) (*textLayeredWindow, error) {
	if err := ensureTextLayerClass(); err != nil {
		return nil, err
	}
	instance := win.GetModuleHandle(nil)
	hwnd := win.CreateWindowEx(
		win.WS_EX_LAYERED|win.WS_EX_TOOLWINDOW|win.WS_EX_TRANSPARENT|win.WS_EX_NOACTIVATE,
		textLayerClassName,
		syscall.StringToUTF16Ptr(""),
		win.WS_POPUP,
		int32(bounds.X),
		int32(bounds.Y),
		int32(max(1, bounds.Width)),
		int32(max(1, bounds.Height)),
		0,
		0,
		instance,
		nil,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx failed: %v", syscall.GetLastError())
	}
	layer := &textLayeredWindow{hwnd: hwnd}
	layer.setBounds(bounds)
	win.ShowWindow(hwnd, win.SW_SHOWNOACTIVATE)
	return layer, nil
}

func ensureTextLayerClass() error {
	textLayerClassOnce.Do(func() {
		instance := win.GetModuleHandle(nil)
		windowClass := win.WNDCLASSEX{
			CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
			LpfnWndProc:   textLayerWndProcCB,
			HInstance:     instance,
			LpszClassName: textLayerClassName,
		}
		if atom := win.RegisterClassEx(&windowClass); atom == 0 {
			textLayerClassErr = fmt.Errorf("RegisterClassEx failed: %v", syscall.GetLastError())
		}
	})
	return textLayerClassErr
}

func textLayeredWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	handle := win.HWND(hwnd)
	switch msg {
	case win.WM_NCHITTEST:
		return ^uintptr(0)
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_PAINT:
		var ps win.PAINTSTRUCT
		hdc := win.BeginPaint(handle, &ps)
		if hdc != 0 {
			win.EndPaint(handle, &ps)
		}
		return 0
	}
	return win.DefWindowProc(handle, msg, wParam, lParam)
}

func (w *textLayeredWindow) setBounds(bounds model.Rect) {
	if w == nil || w.hwnd == 0 {
		return
	}
	win.SetWindowPos(
		w.hwnd,
		win.HWND_TOPMOST,
		int32(bounds.X),
		int32(bounds.Y),
		int32(max(1, bounds.Width)),
		int32(max(1, bounds.Height)),
		win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW,
	)
}

func (w *textLayeredWindow) deactivate() {
	if w == nil || w.hwnd == 0 {
		return
	}
	win.SetWindowPos(
		w.hwnd,
		win.HWND_NOTOPMOST,
		0,
		0,
		0,
		0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE|win.SWP_HIDEWINDOW,
	)
}

func (w *textLayeredWindow) setExcludedFromCapture(excluded bool) {
	if w == nil || w.hwnd == 0 {
		return
	}
	_ = winutil.SetExcludedFromCapture(w.hwnd, excluded)
}

func (w *textLayeredWindow) updateFrame(frame *capture.Frame) error {
	if w == nil || frame == nil || frame.Width <= 0 || frame.Height <= 0 {
		return nil
	}
	if err := w.ensureFrameResources(frame.Width, frame.Height); err != nil {
		return err
	}
	dst := unsafe.Slice((*byte)(w.dibBits), frame.Width*frame.Height*4)
	copyFrameIntoBGRA(dst, frame)
	return nil
}

func (w *textLayeredWindow) present(bounds model.Rect, opacity int) error {
	if w == nil || w.hwnd == 0 || w.dibDC == 0 || w.frameWidth <= 0 || w.frameHeight <= 0 {
		return nil
	}
	w.setBounds(bounds)
	screenDC := win.GetDC(0)
	if screenDC == 0 {
		return fmt.Errorf("GetDC failed: %v", syscall.GetLastError())
	}
	defer win.ReleaseDC(0, screenDC)
	dst := win.POINT{X: int32(bounds.X), Y: int32(bounds.Y)}
	size := win.SIZE{CX: int32(max(1, bounds.Width)), CY: int32(max(1, bounds.Height))}
	src := win.POINT{}
	blend := win.BLENDFUNCTION{
		BlendOp:             acSrcOver,
		BlendFlags:          0,
		SourceConstantAlpha: byte(max(0, min(100, opacity)) * 255 / 100),
		AlphaFormat:         acSrcAlpha,
	}
	result, _, callErr := callUpdateLayeredWindow(w.hwnd, screenDC, &dst, &size, w.dibDC, &src, &blend)
	if result == 0 {
		if callErr == syscall.Errno(0) {
			callErr = fmt.Errorf("unknown UpdateLayeredWindow failure")
		}
		return fmt.Errorf("UpdateLayeredWindow failed: %v", callErr)
	}
	return nil
}

func (w *textLayeredWindow) ensureFrameResources(width int, height int) error {
	if w.dibDC != 0 && w.frameWidth == width && w.frameHeight == height && w.dibBits != nil {
		return nil
	}
	w.dispose()
	hdc := win.CreateCompatibleDC(0)
	if hdc == 0 {
		return fmt.Errorf("CreateCompatibleDC failed")
	}
	bitmapHeader := win.BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(win.BITMAPINFOHEADER{})),
		BiWidth:       int32(width),
		BiHeight:      -int32(height),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: win.BI_RGB,
	}
	var dibBits unsafe.Pointer
	hbitmap := win.CreateDIBSection(hdc, &bitmapHeader, win.DIB_RGB_COLORS, &dibBits, 0, 0)
	if hbitmap == 0 || dibBits == nil {
		win.DeleteDC(hdc)
		return fmt.Errorf("CreateDIBSection failed")
	}
	oldBitmap := win.SelectObject(hdc, win.HGDIOBJ(hbitmap))
	if oldBitmap == 0 {
		win.DeleteObject(win.HGDIOBJ(hbitmap))
		win.DeleteDC(hdc)
		return fmt.Errorf("SelectObject failed")
	}
	w.dibDC = hdc
	w.dibBitmap = hbitmap
	w.dibOldBitmap = oldBitmap
	w.dibBits = dibBits
	w.frameWidth = width
	w.frameHeight = height
	return nil
}

func (w *textLayeredWindow) hide() {
	if w == nil || w.hwnd == 0 {
		return
	}
	win.ShowWindow(w.hwnd, win.SW_HIDE)
}

func (w *textLayeredWindow) show() {
	if w == nil || w.hwnd == 0 {
		return
	}
	win.ShowWindow(w.hwnd, win.SW_SHOWNOACTIVATE)
}

func (w *textLayeredWindow) destroy() {
	if w == nil {
		return
	}
	w.dispose()
	if w.hwnd != 0 {
		win.DestroyWindow(w.hwnd)
		w.hwnd = 0
	}
}

func (w *textLayeredWindow) dispose() {
	if w == nil {
		return
	}
	if w.dibDC != 0 {
		if w.dibOldBitmap != 0 {
			win.SelectObject(w.dibDC, w.dibOldBitmap)
			w.dibOldBitmap = 0
		}
		if w.dibBitmap != 0 {
			win.DeleteObject(win.HGDIOBJ(w.dibBitmap))
			w.dibBitmap = 0
		}
		win.DeleteDC(w.dibDC)
		w.dibDC = 0
	}
	w.dibBits = nil
	w.frameWidth = 0
	w.frameHeight = 0
}
