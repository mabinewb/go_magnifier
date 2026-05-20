package ui

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"

	"github.com/kbinani/screenshot"
	"github.com/lxn/walk"
	"github.com/lxn/win"

	"gomagnifier/internal/model"
	"gomagnifier/internal/winutil"
)

var (
	selectorUser32         = syscall.NewLazyDLL("user32.dll")
	selectorGdi32          = syscall.NewLazyDLL("gdi32.dll")
	procSelectorFillRect   = selectorUser32.NewProc("FillRect")
	procSelectorFrameRect  = selectorUser32.NewProc("FrameRect")
	procSelectorDrawTextW  = selectorUser32.NewProc("DrawTextW")
	procRegisterHotKey     = selectorUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = selectorUser32.NewProc("UnregisterHotKey")
	procSelectorBrush      = selectorGdi32.NewProc("CreateSolidBrush")
)

const (
	selectorHotKeyAccept = 1
	selectorHotKeyCancel = 2
)

type selectorWindow struct {
	*walk.MainWindow

	title   string
	bounds  image.Rectangle
	clientHandle win.HWND

	dragMode selectorDragMode
	resizeEdges int
	dragAnchor image.Point
	dragOrigin model.Rect
	result   model.Rect
	accepted bool
}

type selectorDragMode uint8

const (
	selectorDragNone selectorDragMode = iota
	selectorDragCreate
	selectorDragMove
	selectorDragResize
)

const (
	selectorEdgeLeft = 1 << iota
	selectorEdgeTop
	selectorEdgeRight
	selectorEdgeBottom
)

const selectorResizeMargin = 6

func SelectRect(title string, initial model.Rect) (model.Rect, bool, error) {
	bounds := virtualScreenBounds()
	if bounds.Empty() {
		return model.Rect{}, false, fmt.Errorf("no active display detected")
	}

	mw, err := walk.NewMainWindow()
	if err != nil {
		return model.Rect{}, false, err
	}

	selector := &selectorWindow{
		MainWindow: mw,
		title:      title,
		bounds:     bounds,
		result:     initial,
	}

	mw.SetTitle(title)
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{})
	layout.SetSpacing(0)
	if err := mw.SetLayout(layout); err != nil {
		mw.Dispose()
		return model.Rect{}, false, err
	}
	if err := mw.SetBoundsPixels(walk.Rectangle{X: bounds.Min.X, Y: bounds.Min.Y, Width: bounds.Dx(), Height: bounds.Dy()}); err != nil {
		return model.Rect{}, false, err
	}
	_ = mw.SetDoubleBuffering(true)
	selector.SetCursor(walk.CursorCross())

	selector.SizeChanged().Attach(func() {
		selector.invalidateDisplay()
	})
	selector.Disposing().Attach(func() {
		selector.unregisterHotKeys()
	})

	selector.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		point := clampSelectorPoint(selector.toScreenPoint(x, y), selector.bounds)
		if button != walk.LeftButton {
			return
		}
		selector.dragAnchor = point
		selector.dragOrigin = selector.result
		selector.resizeEdges = selector.hitTestEdges(point)
		switch {
		case !selector.result.Empty() && selector.resizeEdges != 0:
			selector.dragMode = selectorDragResize
		case !selector.result.Empty() && pointInRect(point, selector.result):
			selector.dragMode = selectorDragMove
		default:
			selector.dragMode = selectorDragCreate
			selector.result = normalizeRectInclusive(point, point)
		}
		selector.updateCursor(point)
		selector.invalidateDisplay()
	})

	selector.MouseMove().Attach(func(x, y int, _ walk.MouseButton) {
		point := clampSelectorPoint(selector.toScreenPoint(x, y), selector.bounds)
		if selector.dragMode == selectorDragNone {
			selector.updateCursor(point)
			return
		}
		switch selector.dragMode {
		case selectorDragCreate:
			selector.result = normalizeRectInclusive(selector.dragAnchor, point)
		case selectorDragMove:
			selector.result = moveRectWithinBounds(selector.dragOrigin, point.X-selector.dragAnchor.X, point.Y-selector.dragAnchor.Y, selector.bounds)
		case selectorDragResize:
			selector.result = resizeRectWithinBounds(selector.dragOrigin, selector.resizeEdges, point, selector.bounds)
		}
		selector.updateCursor(point)
		selector.invalidateDisplay()
	})

	selector.MouseUp().Attach(func(x, y int, _ walk.MouseButton) {
		point := clampSelectorPoint(selector.toScreenPoint(x, y), selector.bounds)
		if selector.dragMode == selectorDragNone {
			return
		}
		switch selector.dragMode {
		case selectorDragCreate:
			selector.result = normalizeRectInclusive(selector.dragAnchor, point)
		case selectorDragMove:
			selector.result = moveRectWithinBounds(selector.dragOrigin, point.X-selector.dragAnchor.X, point.Y-selector.dragAnchor.Y, selector.bounds)
		case selectorDragResize:
			selector.result = resizeRectWithinBounds(selector.dragOrigin, selector.resizeEdges, point, selector.bounds)
		}
		selector.dragMode = selectorDragNone
		selector.resizeEdges = 0
		selector.updateCursor(point)
		selector.invalidateDisplay()
	})

	selector.KeyDown().Attach(func(key walk.Key) {
		if key == walk.KeyEscape {
			selector.accepted = false
			selector.Close()
			return
		}
		if key == walk.Key(13) && !selector.result.Empty() {
			selector.accepted = true
			selector.Close()
		}
	})

	winutil.ApplyBorderless(selector.Handle())
	winutil.SetOpacity(selector.Handle(), 74)
	winutil.SetClickThrough(selector.Handle(), false)
	selector.Show()
	selector.clientHandle = findCompositeChildHandle(selector.Handle())
	if err := selector.registerHotKeys(); err != nil {
		mw.Dispose()
		return model.Rect{}, false, err
	}
	if err := installPaintSubclass(selector.Handle(), nil, selector.handleMessage); err != nil {
		mw.Dispose()
		return model.Rect{}, false, err
	}
	if selector.clientHandle != 0 {
		if err := installPaintSubclass(selector.clientHandle, nil, selector.handleMessage); err != nil {
			mw.Dispose()
			return model.Rect{}, false, err
		}
	}
	selector.SetFocus()
	win.SetFocus(selector.Handle())
	selector.Run()

	if !selector.accepted {
		return model.Rect{}, false, nil
	}
	return selector.result, true, nil
}

func (s *selectorWindow) toScreenPoint(x, y int) image.Point {
	return image.Pt(s.bounds.Min.X+x, s.bounds.Min.Y+y)
}

func (s *selectorWindow) selectionRect() model.Rect {
	return s.result
}

func (s *selectorWindow) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (bool, uintptr) {
	switch msg {
	case win.WM_PAINT:
		s.paintNow(hwnd)
		return true, 0
	case win.WM_SETCURSOR:
		var cursorPos win.POINT
		if win.GetCursorPos(&cursorPos) {
			s.updateCursor(image.Pt(int(cursorPos.X), int(cursorPos.Y)))
		}
		return true, 0
	case win.WM_KEYDOWN, win.WM_SYSKEYDOWN:
		switch wParam {
		case win.VK_ESCAPE:
			s.accepted = false
			s.Close()
			return true, 0
		case win.VK_RETURN:
			if !s.result.Empty() {
				s.accepted = true
				s.Close()
			}
			return true, 0
		}
	case win.WM_HOTKEY:
		switch int32(wParam) {
		case selectorHotKeyCancel:
			s.accepted = false
			s.Close()
			return true, 0
		case selectorHotKeyAccept:
			if !s.result.Empty() {
				s.accepted = true
				s.Close()
			}
			return true, 0
		}
	}
	return false, 0
}

func (s *selectorWindow) registerHotKeys() error {
	if s == nil || s.Handle() == 0 {
		return nil
	}
	if !selectorRegisterHotKey(s.Handle(), selectorHotKeyAccept, 0, win.VK_RETURN) {
		return fmt.Errorf("failed to register selector Enter hotkey")
	}
	if !selectorRegisterHotKey(s.Handle(), selectorHotKeyCancel, 0, win.VK_ESCAPE) {
		selectorUnregisterHotKey(s.Handle(), selectorHotKeyAccept)
		return fmt.Errorf("failed to register selector Esc hotkey")
	}
	return nil
}

func (s *selectorWindow) unregisterHotKeys() {
	if s == nil || s.Handle() == 0 {
		return
	}
	selectorUnregisterHotKey(s.Handle(), selectorHotKeyAccept)
	selectorUnregisterHotKey(s.Handle(), selectorHotKeyCancel)
}

func selectorRegisterHotKey(hwnd win.HWND, id int32, modifiers uint32, virtualKey uint32) bool {
	result, _, _ := procRegisterHotKey.Call(uintptr(hwnd), uintptr(id), uintptr(modifiers), uintptr(virtualKey))
	return result != 0
}

func selectorUnregisterHotKey(hwnd win.HWND, id int32) {
	_, _, _ = procUnregisterHotKey.Call(uintptr(hwnd), uintptr(id))
}

func (s *selectorWindow) paintNow(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	if hdc == 0 {
		return
	}
	defer win.EndPaint(hwnd, &ps)

	var clientRect win.RECT
	if !win.GetClientRect(hwnd, &clientRect) {
		return
	}

	bg := selectorCreateSolidBrush(selectorRGB(8, 18, 30))
	if bg != 0 {
		defer win.DeleteObject(win.HGDIOBJ(bg))
		selectorFillRect(hdc, &clientRect, bg)
	}

	selection := s.selectionRect()

	if !selection.Empty() {
		local := win.RECT{
			Left:   int32(selection.X - s.bounds.Min.X),
			Top:    int32(selection.Y - s.bounds.Min.Y),
			Right:  int32(selection.X - s.bounds.Min.X + selection.Width),
			Bottom: int32(selection.Y - s.bounds.Min.Y + selection.Height),
		}

		fill := selectorCreateSolidBrush(selectorRGB(22, 140, 240))
		if fill != 0 {
			defer win.DeleteObject(win.HGDIOBJ(fill))
			selectorFillRect(hdc, &local, fill)
		}

		border := selectorCreateSolidBrush(selectorRGB(240, 250, 255))
		if border != 0 {
			defer win.DeleteObject(win.HGDIOBJ(border))
			selectorFrameRect(hdc, &local, border)
		}
	}

	message := s.title + "\n드래그해서 새 영역을 만들고, 가장자리를 드래그해 크기를 조절할 수 있습니다. 내부 드래그는 이동입니다. Enter로 확정, ESC로 취소할 수 있습니다."
	if !selection.Empty() {
		message += fmt.Sprintf("\n현재 크기: %d x %d  위치: %d, %d", selection.Width, selection.Height, selection.X, selection.Y)
	}

	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(240, 248, 255))
	textRight := clientRect.Right - 24
	if textRight < 24 {
		textRight = 24
	}
	textRect := win.RECT{Left: 24, Top: 24, Right: textRight, Bottom: 156}
	text, err := syscall.UTF16PtrFromString(message)
	if err == nil {
		selectorDrawText(hdc, text, &textRect, win.DT_WORDBREAK)
	}
}

func (s *selectorWindow) invalidateDisplay() {
	if s.clientHandle != 0 {
		win.InvalidateRect(s.clientHandle, nil, false)
	}
	win.InvalidateRect(s.Handle(), nil, false)
}

func selectorRGB(r, g, b byte) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

func selectorCreateSolidBrush(color uint32) win.HBRUSH {
	brush, _, _ := procSelectorBrush.Call(uintptr(color))
	return win.HBRUSH(brush)
}

func selectorFillRect(hdc win.HDC, rect *win.RECT, brush win.HBRUSH) {
	procSelectorFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}

func selectorFrameRect(hdc win.HDC, rect *win.RECT, brush win.HBRUSH) {
	procSelectorFrameRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}

func selectorDrawText(hdc win.HDC, text *uint16, rect *win.RECT, format uint32) {
	procSelectorDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(rect)), uintptr(format))
}

func normalizeRectInclusive(a, b image.Point) model.Rect {
	left := min(a.X, b.X)
	top := min(a.Y, b.Y)
	right := max(a.X, b.X)
	bottom := max(a.Y, b.Y)
	return model.Rect{
		X:      left,
		Y:      top,
		Width:  right - left + 1,
		Height: bottom - top + 1,
	}
}

func clampSelectorPoint(point image.Point, bounds image.Rectangle) image.Point {
	maxX := bounds.Max.X - 1
	maxY := bounds.Max.Y - 1
	if maxX < bounds.Min.X {
		maxX = bounds.Min.X
	}
	if maxY < bounds.Min.Y {
		maxY = bounds.Min.Y
	}
	return image.Pt(clamp(point.X, bounds.Min.X, maxX), clamp(point.Y, bounds.Min.Y, maxY))
}

func pointInRect(point image.Point, rect model.Rect) bool {
	if rect.Empty() {
		return false
	}
	return point.X >= rect.X && point.X < rect.X+rect.Width && point.Y >= rect.Y && point.Y < rect.Y+rect.Height
}

func (s *selectorWindow) hitTestEdges(point image.Point) int {
	selection := s.result
	if selection.Empty() {
		return 0
	}
	left := selection.X
	top := selection.Y
	right := selection.X + selection.Width - 1
	bottom := selection.Y + selection.Height - 1
	if point.X < left-selectorResizeMargin || point.X > right+selectorResizeMargin || point.Y < top-selectorResizeMargin || point.Y > bottom+selectorResizeMargin {
		return 0
	}
	edges := 0
	if abs(point.X-left) <= selectorResizeMargin {
		edges |= selectorEdgeLeft
	}
	if abs(point.X-right) <= selectorResizeMargin {
		edges |= selectorEdgeRight
	}
	if abs(point.Y-top) <= selectorResizeMargin {
		edges |= selectorEdgeTop
	}
	if abs(point.Y-bottom) <= selectorResizeMargin {
		edges |= selectorEdgeBottom
	}
	return edges
}

func (s *selectorWindow) updateCursor(point image.Point) {
	if s == nil {
		return
	}
	if s.dragMode == selectorDragMove {
		s.applyCursor(walk.CursorSizeAll(), win.IDC_SIZEALL)
		return
	}
	edges := s.resizeEdges
	if s.dragMode == selectorDragNone {
		edges = s.hitTestEdges(point)
	}
	switch edges {
	case selectorEdgeLeft, selectorEdgeRight:
		s.applyCursor(walk.CursorSizeWE(), win.IDC_SIZEWE)
	case selectorEdgeTop, selectorEdgeBottom:
		s.applyCursor(walk.CursorSizeNS(), win.IDC_SIZENS)
	case selectorEdgeLeft | selectorEdgeTop, selectorEdgeRight | selectorEdgeBottom:
		s.applyCursor(walk.CursorSizeNWSE(), win.IDC_SIZENWSE)
	case selectorEdgeRight | selectorEdgeTop, selectorEdgeLeft | selectorEdgeBottom:
		s.applyCursor(walk.CursorSizeNESW(), win.IDC_SIZENESW)
	default:
		if pointInRect(point, s.result) && !s.result.Empty() {
			s.applyCursor(walk.CursorSizeAll(), win.IDC_SIZEALL)
			return
		}
		s.applyCursor(walk.CursorCross(), win.IDC_CROSS)
	}
}

func (s *selectorWindow) applyCursor(cursor walk.Cursor, cursorID uintptr) {
	if s == nil {
		return
	}
	s.SetCursor(cursor)
	hCursor := win.LoadCursor(0, win.MAKEINTRESOURCE(cursorID))
	if hCursor != 0 {
		win.SetCursor(hCursor)
	}
}

func moveRectWithinBounds(rect model.Rect, deltaX int, deltaY int, bounds image.Rectangle) model.Rect {
	if rect.Empty() {
		return rect
	}
	maxX := bounds.Max.X - rect.Width
	maxY := bounds.Max.Y - rect.Height
	if maxX < bounds.Min.X {
		maxX = bounds.Min.X
	}
	if maxY < bounds.Min.Y {
		maxY = bounds.Min.Y
	}
	rect.X = clamp(rect.X+deltaX, bounds.Min.X, maxX)
	rect.Y = clamp(rect.Y+deltaY, bounds.Min.Y, maxY)
	return rect
}

func resizeRectWithinBounds(original model.Rect, edges int, point image.Point, bounds image.Rectangle) model.Rect {
	if original.Empty() {
		return original
	}
	left := original.X
	top := original.Y
	right := original.X + original.Width
	bottom := original.Y + original.Height

	if edges&selectorEdgeLeft != 0 {
		left = clamp(point.X, bounds.Min.X, right-1)
	}
	if edges&selectorEdgeTop != 0 {
		top = clamp(point.Y, bounds.Min.Y, bottom-1)
	}
	if edges&selectorEdgeRight != 0 {
		right = clamp(point.X+1, left+1, bounds.Max.X)
	}
	if edges&selectorEdgeBottom != 0 {
		bottom = clamp(point.Y+1, top+1, bounds.Max.Y)
	}

	return model.Rect{X: left, Y: top, Width: right - left, Height: bottom - top}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func virtualScreenBounds() image.Rectangle {
	count := screenshot.NumActiveDisplays()
	if count == 0 {
		return image.Rectangle{}
	}
	bounds := screenshot.GetDisplayBounds(0)
	for index := 1; index < count; index++ {
		bounds = bounds.Union(screenshot.GetDisplayBounds(index))
	}
	return bounds
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}