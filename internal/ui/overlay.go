package ui

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"gomagnifier/internal/capture"
	"gomagnifier/internal/model"
	"gomagnifier/internal/winutil"
)

const (
	scSize         = 0xF000
	wmszLeft       = 1
	wmszRight      = 2
	wmszTop        = 3
	wmszTopLeft    = 4
	wmszTopRight   = 5
	wmszBottom     = 6
	wmszBottomLeft = 7
	wmszBottomRight = 8
	overlayFlushMessage = win.WM_APP + 1
	acSrcOver      = 0x00
	acSrcAlpha     = 0x01
	ulwAlpha       = 0x02
)

var procUpdateLayeredWindow = syscall.NewLazyDLL("user32.dll").NewProc("UpdateLayeredWindow")

type OverlayWindow struct {
	*walk.MainWindow

	mu              sync.Mutex
	queued          *capture.Frame
	flushBusy       atomic.Bool
	clickThrough    bool
	lockAspect      bool
	aspectRatio     float64
	suppressBounds  bool
	appliedBounds   model.Rect
	appliedOpacity  int
	appliedRecursive bool
	usePerPixelAlpha bool
	onBoundsChanged func(model.Rect, bool)
	onPresentError  func(error)
	clientHandle    win.HWND
	textLayer       *textLayeredWindow
	dibDC           win.HDC
	dibBitmap       win.HBITMAP
	dibOldBitmap    win.HGDIOBJ
	dibBits         unsafe.Pointer
	frameWidth      int
	frameHeight     int
	captureExcluded bool
}

type minMaxInfo struct {
	ptReserved    win.POINT
	ptMaxSize     win.POINT
	ptMaxPosition win.POINT
	ptMinTrackSize win.POINT
	ptMaxTrackSize win.POINT
}

func NewOverlayWindow(profile model.Profile) (*OverlayWindow, error) {
	mw, err := walk.NewMainWindow()
	if err != nil {
		return nil, err
	}

	window := &OverlayWindow{MainWindow: mw}
	mw.SetTitle("Go Magnifier Overlay")
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{})
	layout.SetSpacing(0)
	if err := mw.SetLayout(layout); err != nil {
		mw.Dispose()
		return nil, err
	}
	if err := mw.SetBoundsPixels(toWalkRect(profile.OverlayRect)); err != nil {
		return nil, err
	}
	_ = mw.SetDoubleBuffering(true)

	window.SizeChanged().Attach(func() {
		win.InvalidateRect(window.Handle(), nil, false)
	})
	window.Disposing().Attach(func() {
		window.mu.Lock()
		textLayer := window.textLayer
		window.textLayer = nil
		window.disposeFrameResourcesLocked()
		window.mu.Unlock()
		if textLayer != nil {
			textLayer.destroy()
		}
	})

	window.clickThrough = profile.ClickThrough
	window.lockAspect = profile.LockAspect
	window.aspectRatio = profile.EffectiveAspectRatio()
	window.appliedBounds = profile.OverlayRect
	window.appliedOpacity = profile.Opacity
	window.appliedRecursive = profile.BlockRecursiveCapture
	window.usePerPixelAlpha = profile.SourceKind == model.SourceText
	winutil.ApplyBorderless(window.Handle())
	if !window.usePerPixelAlpha {
		winutil.SetOpacity(window.Handle(), profile.Opacity)
	}
	winutil.SetClickThrough(window.Handle(), profile.ClickThrough)
	window.Show()
	window.clientHandle = findCompositeChildHandle(window.Handle())
	window.updateChildVisibility()
	window.fitClientHandle()
	window.applyClientClickThrough(profile.ClickThrough)
	if err := installPaintSubclass(window.Handle(), nil, window.handleTopLevelMessage); err != nil {
		mw.Dispose()
		return nil, err
	}
	if window.clientHandle != 0 {
		if err := installPaintSubclass(window.clientHandle, nil, window.handleClientMessage); err != nil {
			mw.Dispose()
			return nil, err
		}
	}
	if window.clientHandle == 0 {
		if err := installPaintSubclass(window.Handle(), nil, window.handleClientMessage); err != nil {
			mw.Dispose()
			return nil, err
		}
	}
	if err := window.updateTextPresentation(profile); err != nil {
		mw.Dispose()
		return nil, err
	}
	if err := mw.SetBoundsPixels(toWalkRect(profile.OverlayRect)); err != nil {
		mw.Dispose()
		return nil, err
	}
	window.fitClientHandle()
	if window.usesPerPixelAlpha() {
		window.syncTextLayerBounds()
	}

	return window, nil
}

func (o *OverlayWindow) ApplyProfile(profile model.Profile) error {
	profile = profile.Sanitized()
	boundsChanged := !rectEquals(o.appliedBounds, profile.OverlayRect)
	if boundsChanged {
		o.mu.Lock()
		o.suppressBounds = true
		o.mu.Unlock()
		if err := o.SetBoundsPixels(toWalkRect(profile.OverlayRect)); err != nil {
			o.mu.Lock()
			o.suppressBounds = false
			o.mu.Unlock()
			return err
		}
	}
	o.mu.Lock()
	clickThroughChanged := o.clickThrough != profile.ClickThrough
	opacityChanged := o.appliedOpacity != profile.Opacity
	layeredModeChanged := o.usePerPixelAlpha != (profile.SourceKind == model.SourceText)
	o.clickThrough = profile.ClickThrough
	o.lockAspect = profile.LockAspect
	o.aspectRatio = profile.EffectiveAspectRatio()
	o.appliedBounds = profile.OverlayRect
	o.appliedOpacity = profile.Opacity
	o.appliedRecursive = profile.BlockRecursiveCapture
	o.usePerPixelAlpha = profile.SourceKind == model.SourceText
	o.suppressBounds = false
	o.mu.Unlock()
	if layeredModeChanged {
		resetLayeredRenderingMode(o.Handle())
		o.updateChildVisibility()
	}
	if boundsChanged {
		o.fitClientHandle()
	}
	if err := o.updateTextPresentation(profile); err != nil {
		return err
	}
	if opacityChanged {
		if o.usesPerPixelAlpha() {
			o.presentLayeredFrame()
		} else {
			winutil.SetOpacity(o.Handle(), profile.Opacity)
		}
	}
	if clickThroughChanged {
		winutil.SetClickThrough(o.Handle(), profile.ClickThrough)
		o.applyClientClickThrough(profile.ClickThrough)
	}
	if o.usesPerPixelAlpha() {
		o.presentLayeredFrame()
	} else {
		o.invalidateDisplay()
	}
	return nil
}

func (o *OverlayWindow) SetBoundsChanged(handler func(model.Rect, bool)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onBoundsChanged = handler
}

func (o *OverlayWindow) SetPresentErrorHandler(handler func(error)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onPresentError = handler
}

func (o *OverlayWindow) SetCaptureExcluded(excluded bool) error {
	o.mu.Lock()
	if o.captureExcluded == excluded {
		o.mu.Unlock()
		return nil
	}
	o.captureExcluded = excluded
	textLayer := o.textLayer
	hwnd := o.Handle()
	o.mu.Unlock()
	if err := winutil.SetExcludedFromCapture(hwnd, excluded); err != nil {
		o.mu.Lock()
		o.captureExcluded = !excluded
		o.mu.Unlock()
		return err
	}
	if textLayer != nil {
		textLayer.setExcludedFromCapture(excluded)
	}
	return nil
}

func (o *OverlayWindow) PrepareForClose() {
	o.mu.Lock()
	textLayer := o.textLayer
	o.textLayer = nil
	o.captureExcluded = false
	o.mu.Unlock()

	if textLayer != nil {
		textLayer.deactivate()
		textLayer.destroy()
	}
	_ = winutil.SetExcludedFromCapture(o.Handle(), false)
	winutil.SetAlwaysOnTop(o.Handle(), false)
	win.ShowWindow(o.Handle(), win.SW_HIDE)
	if o.MainWindow != nil {
		o.SetVisible(false)
	}
}

func (o *OverlayWindow) SubmitFrame(frame *capture.Frame) {
	if frame == nil {
		return
	}

	o.mu.Lock()
	o.queued = frame
	o.mu.Unlock()

	if o.flushBusy.CompareAndSwap(false, true) {
		win.PostMessage(o.Handle(), overlayFlushMessage, 0, 0)
	}
}

func (o *OverlayWindow) PresentFrame(frame *capture.Frame) error {
	if frame == nil {
		return nil
	}
	if err := o.updateFrameBuffer(frame); err != nil {
		return err
	}
	if o.usesPerPixelAlpha() {
		o.presentLayeredFrame()
	} else {
		o.invalidateDisplay()
	}
	return nil
}

func (o *OverlayWindow) flushQueuedFrame() {
	defer o.flushBusy.Store(false)

	for {
		o.mu.Lock()
		frame := o.queued
		o.queued = nil
		o.mu.Unlock()

		if frame == nil {
			break
		}

		if err := o.updateFrameBuffer(frame); err != nil {
			if handler := o.presentErrorHandler(); handler != nil {
				handler(err)
			}
			continue
		}
	}

	if o.usesPerPixelAlpha() {
		o.presentLayeredFrame()
	} else {
		o.invalidateDisplay()
	}

	o.mu.Lock()
	hasQueued := o.queued != nil
	o.mu.Unlock()
	if hasQueued && o.flushBusy.CompareAndSwap(false, true) {
		win.PostMessage(o.Handle(), overlayFlushMessage, 0, 0)
	}
}


func (o *OverlayWindow) handleTopLevelMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (bool, uintptr) {
	switch msg {
	case win.WM_ERASEBKGND:
		return true, 1
	case win.WM_GETMINMAXINFO:
		info := (*minMaxInfo)(unsafe.Pointer(lParam))
		if info != nil {
			info.ptMinTrackSize.X = int32(model.MinOverlaySize)
			info.ptMinTrackSize.Y = int32(model.MinOverlaySize)
			return true, 0
		}
	case win.WM_SETCURSOR:
		if o.isClickThrough() {
			return false, 0
		}
		if o.updateCursorShape() {
			return true, 1
		}
	case win.WM_NCHITTEST:
		if o.isClickThrough() {
			return true, ^uintptr(0)
		}
		return true, uintptr(o.hitTest(win.GET_X_LPARAM(lParam), win.GET_Y_LPARAM(lParam)))
	case 0x0214:
		if !o.aspectLocked() {
			if o.usesPerPixelAlpha() {
				o.syncTextLayerBounds()
				o.presentLayeredFrame()
			}
			if liveRect := rectFromSizingMessage(lParam); !liveRect.Empty() {
				o.notifyBoundsChangedWithRect(liveRect, false)
			}
			return false, 0
		}
		o.applySizingAspect(uint32(wParam), lParam)
		if liveRect := rectFromSizingMessage(lParam); !liveRect.Empty() {
			if o.usesPerPixelAlpha() && o.textLayer != nil {
				o.textLayer.setBounds(liveRect)
			}
			o.notifyBoundsChangedWithRect(liveRect, false)
		}
		if o.usesPerPixelAlpha() {
			o.syncTextLayerBounds()
			o.presentLayeredFrame()
		}
		return true, 1
	case win.WM_PAINT:
		if o.usesPerPixelAlpha() {
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(hwnd, &ps)
			if hdc != 0 {
				win.EndPaint(hwnd, &ps)
			}
			return true, 0
		}
		o.paintFrame(hwnd)
		return true, 0
	case overlayFlushMessage:
		o.flushQueuedFrame()
		return true, 0
	case win.WM_SIZE:
		o.fitClientHandle()
		if o.usesPerPixelAlpha() {
			o.syncTextLayerBounds()
			o.presentLayeredFrame()
		} else {
			o.invalidateDisplay()
		}
		o.notifyBoundsChangedWithRect(o.currentBounds(), true)
	case win.WM_MOVE:
		if o.usesPerPixelAlpha() {
			o.syncTextLayerBounds()
			o.presentLayeredFrame()
		}
		o.notifyBoundsChangedWithRect(o.currentBounds(), false)
	case win.WM_MOVING, win.WM_WINDOWPOSCHANGED:
		liveRect := rectFromMovingMessage(lParam)
		if msg == win.WM_WINDOWPOSCHANGED {
			liveRect = rectFromWindowPosMessage(lParam)
		}
		if !liveRect.Empty() && o.usesPerPixelAlpha() && o.textLayer != nil {
			o.textLayer.setBounds(liveRect)
		}
		if o.usesPerPixelAlpha() {
			o.syncTextLayerBounds()
			o.presentLayeredFrame()
		}
		if !liveRect.Empty() {
			o.notifyBoundsChangedWithRect(liveRect, false)
		}
	case win.WM_LBUTTONDOWN:
		if o.isClickThrough() {
			return false, 0
		}
		win.ReleaseCapture()
		win.SendMessage(o.Handle(), win.WM_NCLBUTTONDOWN, uintptr(win.HTCAPTION), 0)
		return true, 0
	case win.WM_EXITSIZEMOVE:
		if o.usesPerPixelAlpha() {
			o.syncTextLayerBounds()
			o.presentLayeredFrame()
		}
		o.notifyBoundsChangedWithRect(o.currentBounds(), true)
	}
	return false, 0
}

func (o *OverlayWindow) shouldNotifyBounds() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.suppressBounds
}

func (o *OverlayWindow) handleClientMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (bool, uintptr) {
	switch msg {
	case win.WM_ERASEBKGND:
		return true, 1
	case win.WM_SETCURSOR:
		if o.isClickThrough() {
			return false, 0
		}
		if o.updateCursorShape() {
			return true, 1
		}
	case win.WM_PAINT:
		if o.usesPerPixelAlpha() {
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(hwnd, &ps)
			if hdc != 0 {
				win.EndPaint(hwnd, &ps)
			}
			return true, 0
		}
		o.paintFrame(hwnd)
		return true, 0
	case win.WM_LBUTTONDOWN:
		if o.isClickThrough() {
			return false, 0
		}
		var rect win.RECT
		if win.GetWindowRect(hwnd, &rect) {
			hit := o.hitTest(rect.Left+win.GET_X_LPARAM(lParam), rect.Top+win.GET_Y_LPARAM(lParam))
			if cmd, ok := resizeCommandForHit(hit); ok {
				win.ReleaseCapture()
				win.SendMessage(o.Handle(), win.WM_SYSCOMMAND, cmd, 0)
				return true, 0
			}
		}
		win.ReleaseCapture()
		win.SendMessage(o.Handle(), win.WM_NCLBUTTONDOWN, uintptr(win.HTCAPTION), 0)
		return true, 0
	}
	return false, 0
}

func (o *OverlayWindow) paintFrame(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	if hdc == 0 {
		return
	}
	defer win.EndPaint(hwnd, &ps)

	o.mu.Lock()
	dibDC := o.dibDC
	frameWidth := o.frameWidth
	frameHeight := o.frameHeight
	o.mu.Unlock()
	if dibDC == 0 || frameWidth <= 0 || frameHeight <= 0 {
		return
	}

	var clientRect win.RECT
	if !win.GetClientRect(hwnd, &clientRect) {
		return
	}

	win.SetStretchBltMode(hdc, win.HALFTONE)
	win.StretchBlt(
		hdc,
		0,
		0,
		clientRect.Right-clientRect.Left,
		clientRect.Bottom-clientRect.Top,
		dibDC,
		0,
		0,
		int32(frameWidth),
		int32(frameHeight),
		win.SRCCOPY,
	)
}


func (o *OverlayWindow) updateFrameBuffer(frame *capture.Frame) error {
	width := frame.Width
	height := frame.Height
	if width <= 0 || height <= 0 {
		return nil
	}
	o.mu.Lock()
	textLayer := o.textLayer
	o.mu.Unlock()
	if textLayer != nil {
		if err := textLayer.updateFrame(frame); err != nil {
			return err
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if err := o.ensureFrameResourcesLocked(width, height); err != nil {
		return err
	}

	dst := unsafe.Slice((*byte)(o.dibBits), width*height*4)
	copyFrameIntoBGRA(dst, frame)
	return nil
}

func (o *OverlayWindow) ensureFrameResourcesLocked(width, height int) error {
	if o.dibDC != 0 && o.frameWidth == width && o.frameHeight == height && o.dibBits != nil {
		return nil
	}

	o.disposeFrameResourcesLocked()

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

	o.dibDC = hdc
	o.dibBitmap = hbitmap
	o.dibOldBitmap = oldBitmap
	o.dibBits = dibBits
	o.frameWidth = width
	o.frameHeight = height
	return nil
}

func (o *OverlayWindow) disposeFrameResourcesLocked() {
	if o.dibDC != 0 {
		if o.dibOldBitmap != 0 {
			win.SelectObject(o.dibDC, o.dibOldBitmap)
			o.dibOldBitmap = 0
		}
		if o.dibBitmap != 0 {
			win.DeleteObject(win.HGDIOBJ(o.dibBitmap))
			o.dibBitmap = 0
		}
		win.DeleteDC(o.dibDC)
		o.dibDC = 0
	}
	o.dibBits = nil
	o.frameWidth = 0
	o.frameHeight = 0
}

func (o *OverlayWindow) isClickThrough() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.clickThrough
}

func (o *OverlayWindow) boundsChangedHandler() func(model.Rect, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.onBoundsChanged
}

func (o *OverlayWindow) presentErrorHandler() func(error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.onPresentError
}

func (o *OverlayWindow) aspectLocked() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lockAspect
}

func (o *OverlayWindow) lockedAspectRatio() float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.aspectRatio <= 0 {
		return 1
	}
	return o.aspectRatio
}

func (o *OverlayWindow) currentBounds() model.Rect {
	var rect win.RECT
	if !win.GetWindowRect(o.Handle(), &rect) {
		return model.Rect{}
	}
	return model.Rect{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}
}

func (o *OverlayWindow) hitTest(screenX, screenY int32) int32 {
	var rect win.RECT
	if !win.GetWindowRect(o.Handle(), &rect) {
		return win.HTCLIENT
	}

	border := max(8, int(win.GetSystemMetrics(win.SM_CXSIZEFRAME)))
	left := int(screenX) < int(rect.Left)+border
	right := int(screenX) >= int(rect.Right)-border
	top := int(screenY) < int(rect.Top)+border
	bottom := int(screenY) >= int(rect.Bottom)-border

	switch {
	case top && left:
		return win.HTTOPLEFT
	case top && right:
		return win.HTTOPRIGHT
	case bottom && left:
		return win.HTBOTTOMLEFT
	case bottom && right:
		return win.HTBOTTOMRIGHT
	case left:
		return win.HTLEFT
	case right:
		return win.HTRIGHT
	case top:
		return win.HTTOP
	case bottom:
		return win.HTBOTTOM
	default:
		return win.HTCLIENT
	}
}

func (o *OverlayWindow) invalidateDisplay() {
	if o.clientHandle != 0 {
		win.InvalidateRect(o.clientHandle, nil, false)
	}
	win.InvalidateRect(o.Handle(), nil, false)
}

func (o *OverlayWindow) notifyBoundsChangedWithRect(rect model.Rect, final bool) {
	if !o.shouldNotifyBounds() || rect.Empty() {
		return
	}
	if handler := o.boundsChangedHandler(); handler != nil {
		handler(rect, final)
	}
}

func (o *OverlayWindow) fitClientHandle() {
	if o.clientHandle == 0 {
		return
	}
	var rect win.RECT
	if !win.GetClientRect(o.Handle(), &rect) {
		return
	}
	win.SetWindowPos(
		o.clientHandle,
		0,
		0,
		0,
		rect.Right-rect.Left,
		rect.Bottom-rect.Top,
		win.SWP_NOZORDER|win.SWP_NOACTIVATE,
	)
}

func (o *OverlayWindow) updateChildVisibility() {
	show := uintptr(win.SW_HIDE)
	enumProc := syscall.NewCallback(func(child uintptr, _ uintptr) uintptr {
		win.ShowWindow(win.HWND(child), int32(show))
		return 1
	})
	win.EnumChildWindows(o.Handle(), enumProc, 0)
}

func (o *OverlayWindow) applyClientClickThrough(enabled bool) {
	if o.clientHandle == 0 {
		return
	}
	exStyle := uint32(win.GetWindowLong(o.clientHandle, win.GWL_EXSTYLE))
	if enabled {
		exStyle |= win.WS_EX_TRANSPARENT
	} else {
		exStyle &^= win.WS_EX_TRANSPARENT
	}
	win.SetWindowLong(o.clientHandle, win.GWL_EXSTYLE, int32(exStyle))
	win.SetWindowPos(o.clientHandle, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

func findCompositeChildHandle(parent win.HWND) win.HWND {
	const compositeClassName = `\o/ Walk_Composite_Class \o/`
	var found win.HWND
	enumProc := syscall.NewCallback(func(child uintptr, _ uintptr) uintptr {
		if found != 0 {
			return 0
		}
		buf := make([]uint16, 128)
		count, err := win.GetClassName(win.HWND(child), &buf[0], len(buf))
		if err != nil || count == 0 {
			return 1
		}
		className := syscall.UTF16ToString(buf[:count])
		if strings.EqualFold(className, compositeClassName) {
			found = win.HWND(child)
			return 0
		}
		return 1
	})
	win.EnumChildWindows(parent, enumProc, 0)
	return found
}

func copyFrameIntoBGRA(dst []byte, frame *capture.Frame) {
	if frame == nil {
		return
	}
	width := frame.Width
	for y := 0; y < frame.Height; y++ {
		srcRow := frame.Pix[y*frame.Stride : y*frame.Stride+width*4]
		dstRow := dst[y*width*4 : (y+1)*width*4]
		if frame.Format == capture.PixelFormatBGRA {
			copy(dstRow, srcRow)
			continue
		}
		for offset := 0; offset < len(srcRow); offset += 4 {
			dstRow[offset] = srcRow[offset+2]
			dstRow[offset+1] = srcRow[offset+1]
			dstRow[offset+2] = srcRow[offset]
			dstRow[offset+3] = srcRow[offset+3]
		}
	}
}

func toWalkRect(rect model.Rect) walk.Rectangle {
	return walk.Rectangle{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
}

func (o *OverlayWindow) applySizingAspect(edge uint32, lParam uintptr) {
	rect := (*win.RECT)(unsafe.Pointer(lParam))
	if rect == nil {
		return
	}

	current := model.Rect{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}
	ratio := o.lockedAspectRatio()
	if ratio <= 0 {
		return
	}

	newWidth := max(model.MinOverlaySize, current.Width)
	newHeight := max(model.MinOverlaySize, current.Height)
	widthFromHeight := max(model.MinOverlaySize, int(math.Round(float64(newHeight)*ratio)))
	heightFromWidth := max(model.MinOverlaySize, int(math.Round(float64(newWidth)/ratio)))

	switch edge {
	case wmszLeft, wmszRight:
		newHeight = heightFromWidth
	case wmszTop, wmszBottom:
		newWidth = widthFromHeight
	default:
		widthError := math.Abs(float64(widthFromHeight - newWidth))
		heightError := math.Abs(float64(heightFromWidth - newHeight))
		if widthError <= heightError {
			newWidth = widthFromHeight
		} else {
			newHeight = heightFromWidth
		}
	}

	switch edge {
	case wmszLeft:
		rect.Left = rect.Right - int32(newWidth)
		rect.Top = rect.Bottom - int32(newHeight)
	case wmszRight:
		rect.Top = rect.Bottom - int32(newHeight)
	case wmszTop:
		rect.Right = rect.Left + int32(newWidth)
		rect.Top = rect.Bottom - int32(newHeight)
	case wmszTopLeft:
		rect.Left = rect.Right - int32(newWidth)
		rect.Top = rect.Bottom - int32(newHeight)
	case wmszTopRight:
		rect.Right = rect.Left + int32(newWidth)
		rect.Top = rect.Bottom - int32(newHeight)
	case wmszBottom:
		rect.Left = rect.Right - int32(newWidth)
		rect.Bottom = rect.Top + int32(newHeight)
	case wmszBottomLeft:
		rect.Left = rect.Right - int32(newWidth)
		rect.Bottom = rect.Top + int32(newHeight)
	case wmszBottomRight:
		rect.Right = rect.Left + int32(newWidth)
		rect.Bottom = rect.Top + int32(newHeight)
	}
}

func resizeCommandForHit(hit int32) (uintptr, bool) {
	switch hit {
	case win.HTLEFT:
		return scSize + wmszLeft, true
	case win.HTRIGHT:
		return scSize + wmszRight, true
	case win.HTTOP:
		return scSize + wmszTop, true
	case win.HTTOPLEFT:
		return scSize + wmszTopLeft, true
	case win.HTTOPRIGHT:
		return scSize + wmszTopRight, true
	case win.HTBOTTOM:
		return scSize + wmszBottom, true
	case win.HTBOTTOMLEFT:
		return scSize + wmszBottomLeft, true
	case win.HTBOTTOMRIGHT:
		return scSize + wmszBottomRight, true
	default:
		return 0, false
	}
}

func (o *OverlayWindow) updateCursorShape() bool {
	var point win.POINT
	if !win.GetCursorPos(&point) {
		return false
	}
	hit := o.hitTest(point.X, point.Y)
	var cursorID uintptr
	switch hit {
	case win.HTLEFT, win.HTRIGHT:
		cursorID = win.IDC_SIZEWE
	case win.HTTOP, win.HTBOTTOM:
		cursorID = win.IDC_SIZENS
	case win.HTTOPLEFT, win.HTBOTTOMRIGHT:
		cursorID = win.IDC_SIZENWSE
	case win.HTTOPRIGHT, win.HTBOTTOMLEFT:
		cursorID = win.IDC_SIZENESW
	default:
		cursorID = win.IDC_ARROW
	}
	win.SetCursor(win.LoadCursor(0, win.MAKEINTRESOURCE(cursorID)))
	return true
}

func rectEquals(left, right model.Rect) bool {
	return left.X == right.X && left.Y == right.Y && left.Width == right.Width && left.Height == right.Height
}

func (o *OverlayWindow) usesPerPixelAlpha() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.usePerPixelAlpha
}

func (o *OverlayWindow) presentLayeredFrame() {
	if o.textLayer != nil {
		o.mu.Lock()
		bounds := o.presentationBoundsLocked()
		opacity := o.appliedOpacity
		textLayer := o.textLayer
		o.mu.Unlock()
		if err := textLayer.present(bounds, opacity); err != nil {
			o.fallbackFromLayeredMode(fmt.Errorf("텍스트 오버레이 고급 합성 실패: %v", err))
		}
		return
	}
	o.mu.Lock()
	dibDC := o.dibDC
	frameWidth := o.frameWidth
	frameHeight := o.frameHeight
	bounds := o.appliedBounds
	opacity := o.appliedOpacity
	o.mu.Unlock()
	if dibDC == 0 || frameWidth <= 0 || frameHeight <= 0 {
		return
	}
	if bounds.Width <= 0 || bounds.Height <= 0 {
		bounds = o.currentBounds()
	}
	if bounds.Width <= 0 || bounds.Height <= 0 {
		bounds.Width = frameWidth
		bounds.Height = frameHeight
	}
	screenDC := win.GetDC(0)
	if screenDC == 0 {
		o.fallbackFromLayeredMode(fmt.Errorf("UpdateLayeredWindow 준비 실패: screen DC를 가져오지 못했습니다."))
		return
	}
	defer win.ReleaseDC(0, screenDC)
	dst := win.POINT{X: int32(bounds.X), Y: int32(bounds.Y)}
	size := win.SIZE{CX: int32(bounds.Width), CY: int32(bounds.Height)}
	src := win.POINT{}
	blend := win.BLENDFUNCTION{
		BlendOp:             acSrcOver,
		BlendFlags:          0,
		SourceConstantAlpha: byte(max(0, min(100, opacity)) * 255 / 100),
		AlphaFormat:         acSrcAlpha,
	}
	result, _, callErr := callUpdateLayeredWindow(o.Handle(), screenDC, &dst, &size, dibDC, &src, &blend)
	if result == 0 {
		resetLayeredRenderingMode(o.Handle())
		o.updateChildVisibility()
		result, _, callErr = callUpdateLayeredWindow(o.Handle(), screenDC, &dst, &size, dibDC, &src, &blend)
		if result == 0 {
			if callErr == syscall.Errno(0) {
				callErr = fmt.Errorf("unknown UpdateLayeredWindow failure")
			}
			o.fallbackFromLayeredMode(fmt.Errorf("텍스트 오버레이 고급 합성 실패: %v", callErr))
		}
	}
}

func callUpdateLayeredWindow(hwnd win.HWND, screenDC win.HDC, dst *win.POINT, size *win.SIZE, srcDC win.HDC, src *win.POINT, blend *win.BLENDFUNCTION) (uintptr, uintptr, error) {
	result, callResult, callErr := procUpdateLayeredWindow.Call(
		uintptr(hwnd),
		uintptr(screenDC),
		uintptr(unsafe.Pointer(dst)),
		uintptr(unsafe.Pointer(size)),
		uintptr(srcDC),
		uintptr(unsafe.Pointer(src)),
		0,
		uintptr(unsafe.Pointer(blend)),
		uintptr(ulwAlpha),
	)
	return result, callResult, callErr
}

func (o *OverlayWindow) fallbackFromLayeredMode(err error) {
	o.mu.Lock()
	if !o.usePerPixelAlpha {
		o.mu.Unlock()
		return
	}
	o.usePerPixelAlpha = false
	handler := o.onPresentError
	opacity := o.appliedOpacity
	textLayer := o.textLayer
	o.textLayer = nil
	o.mu.Unlock()
	if textLayer != nil {
		textLayer.destroy()
	}
	o.updateChildVisibility()
	winutil.SetOpacity(o.Handle(), opacity)
	o.invalidateDisplay()
	appendRuntimeLog("ERROR", "overlay layered fallback: "+err.Error())
	if handler != nil && err != nil {
		handler(err)
	}
}

func resetLayeredRenderingMode(hwnd win.HWND) {
	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle&^win.WS_EX_LAYERED))
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle|win.WS_EX_LAYERED))
	win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
}

func (o *OverlayWindow) updateTextPresentation(profile model.Profile) error {
	if profile.SourceKind != model.SourceText {
		o.disableTextLayer()
		winutil.SetOpacity(o.Handle(), profile.Opacity)
		return nil
	}
	if err := o.ensureTextLayer(profile); err != nil {
		return err
	}
	winutil.SetOpacity(o.Handle(), 1)
	o.updateChildVisibility()
	o.syncTextLayerBounds()
	return nil
}

func (o *OverlayWindow) ensureTextLayer(profile model.Profile) error {
	o.mu.Lock()
	textLayer := o.textLayer
	o.mu.Unlock()
	if textLayer == nil {
		created, err := newTextLayeredWindow(profile.OverlayRect)
		if err != nil {
			return err
		}
		o.mu.Lock()
		o.textLayer = created
		o.mu.Unlock()
		return nil
	}
	textLayer.setBounds(profile.OverlayRect)
	return nil
}

func (o *OverlayWindow) disableTextLayer() {
	o.mu.Lock()
	textLayer := o.textLayer
	o.textLayer = nil
	o.mu.Unlock()
	if textLayer != nil {
		textLayer.destroy()
	}
	o.updateChildVisibility()
}

func (o *OverlayWindow) syncTextLayerBounds() {
	o.mu.Lock()
	textLayer := o.textLayer
	bounds := o.presentationBoundsLocked()
	o.mu.Unlock()
	if textLayer == nil {
		return
	}
	textLayer.setBounds(bounds)
}

func (o *OverlayWindow) presentationBoundsLocked() model.Rect {
	bounds := o.appliedBounds
	if !o.suppressBounds {
		var rect win.RECT
		if win.GetWindowRect(o.Handle(), &rect) {
			liveBounds := model.Rect{
				X:      int(rect.Left),
				Y:      int(rect.Top),
				Width:  int(rect.Right - rect.Left),
				Height: int(rect.Bottom - rect.Top),
			}
			if !liveBounds.Empty() {
				bounds = liveBounds
			}
		}
	}
	if bounds.Empty() {
		bounds = model.Rect{Width: max(1, o.frameWidth), Height: max(1, o.frameHeight)}
	}
	return bounds
}

func rectFromSizingMessage(lParam uintptr) model.Rect {
	rect := (*win.RECT)(unsafe.Pointer(lParam))
	if rect == nil {
		return model.Rect{}
	}
	return model.Rect{X: int(rect.Left), Y: int(rect.Top), Width: int(rect.Right - rect.Left), Height: int(rect.Bottom - rect.Top)}
}

func rectFromMovingMessage(lParam uintptr) model.Rect {
	rect := (*win.RECT)(unsafe.Pointer(lParam))
	if rect == nil {
		return model.Rect{}
	}
	return model.Rect{X: int(rect.Left), Y: int(rect.Top), Width: int(rect.Right - rect.Left), Height: int(rect.Bottom - rect.Top)}
}

func rectFromWindowPosMessage(lParam uintptr) model.Rect {
	windowPos := (*win.WINDOWPOS)(unsafe.Pointer(lParam))
	if windowPos == nil {
		return model.Rect{}
	}
	if (windowPos.Flags&win.SWP_NOMOVE) != 0 && (windowPos.Flags&win.SWP_NOSIZE) != 0 {
		return model.Rect{}
	}
	return model.Rect{X: int(windowPos.X), Y: int(windowPos.Y), Width: int(windowPos.Cx), Height: int(windowPos.Cy)}
}