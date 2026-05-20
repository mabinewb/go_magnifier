//go:build windows

package capture

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/win"

	"gomagnifier/internal/model"
)

var (
	textUser32              = syscall.NewLazyDLL("user32.dll")
	textGdi32               = syscall.NewLazyDLL("gdi32.dll")
	procTextFillRect        = textUser32.NewProc("FillRect")
	procTextDrawTextW       = textUser32.NewProc("DrawTextW")
	procTextCreateSolidBrush = textGdi32.NewProc("CreateSolidBrush")
	procTextCreateFontW     = textGdi32.NewProc("CreateFontW")
)

func renderTextFrame(profile model.Profile) (*Frame, error) {
	width := max(64, profile.CaptureRect.Width)
	height := max(64, profile.CaptureRect.Height)
	bgColor := parseTextColor(profile.TextBackground, 0x102030)
	fgColor := parseTextColor(profile.TextColor, 0xF0F8FF)

	fontSize := profile.TextFontSize
	if fontSize <= 0 {
		fontSize = 36
	}
	font, err := createTextFont(profile.TextFontFamily, fontSize, profile.TextBold, profile.TextItalic)
	if err != nil {
		return nil, err
	}
	defer win.DeleteObject(win.HGDIOBJ(font))

	padding := int32(16)
	contentRect := win.RECT{Left: padding, Top: padding, Right: int32(width) - padding, Bottom: int32(height) - padding}
	text, err := syscall.UTF16PtrFromString(profile.TextContent)
	if err != nil {
		return nil, fmt.Errorf("text encoding failed: %w", err)
	}
	flags := textDrawFlags(profile)
	calcRect, err := measureTextRect(width, height, font, text, contentRect, flags)
	if err != nil {
		return nil, err
	}
	textHeight := calcRect.Bottom - calcRect.Top
	availableHeight := contentRect.Bottom - contentRect.Top
	textRect := contentRect
	switch profile.TextAlignY {
	case model.TextAlignBottom:
		textRect.Top = contentRect.Bottom - textHeight
	case model.TextAlignMiddle:
		textRect.Top = contentRect.Top + (availableHeight-textHeight)/2
	default:
		textRect.Top = contentRect.Top
	}
	if textRect.Top < contentRect.Top {
		textRect.Top = contentRect.Top
	}
	textRect.Bottom = contentRect.Bottom
	raw := make([]byte, width*height*4)
	applyBackgroundColor(raw, bgColor, profile.TextBackgroundAlpha)
	if profile.TextShadow {
		outlineColor := parseTextColor(profile.TextShadowColor, 0x000000)
		outlineSize := max(1, profile.TextShadowOffset)
		for offsetY := -outlineSize; offsetY <= outlineSize; offsetY++ {
			for offsetX := -outlineSize; offsetX <= outlineSize; offsetX++ {
				if offsetX == 0 && offsetY == 0 {
					continue
				}
				outlineRect := textRect
				outlineRect.Left += int32(offsetX)
				outlineRect.Right += int32(offsetX)
				outlineRect.Top += int32(offsetY)
				outlineRect.Bottom += int32(offsetY)
				outlineMask, maskErr := renderTextMask(width, height, font, text, &outlineRect, flags)
				if maskErr != nil {
					return nil, maskErr
				}
				blendTextMask(raw, outlineMask, outlineColor)
			}
		}
	}
	textMask, err := renderTextMask(width, height, font, text, &textRect, flags)
	if err != nil {
		return nil, err
	}
	blendTextMask(raw, textMask, fgColor)
	return cloneBGRAFrame(raw, width, height), nil
}

func measureTextRect(width int, height int, font win.HFONT, text *uint16, rect win.RECT, flags uint32) (win.RECT, error) {
	hdc := win.CreateCompatibleDC(0)
	if hdc == 0 {
		return win.RECT{}, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer win.DeleteDC(hdc)
	oldFont := win.SelectObject(hdc, win.HGDIOBJ(font))
	if oldFont != 0 {
		defer win.SelectObject(hdc, oldFont)
	}
	measureRect := rect
	textDraw(hdc, text, &measureRect, flags|win.DT_CALCRECT)
	return measureRect, nil
}

func applyBackgroundColor(raw []byte, bgColor uint32, backgroundAlpha int) {
	if len(raw) == 0 {
		return
	}
	if backgroundAlpha < 0 {
		backgroundAlpha = 0
	}
	if backgroundAlpha > 100 {
		backgroundAlpha = 100
	}
	alpha := byte(backgroundAlpha * 255 / 100)
	bgB := byte(bgColor >> 16)
	bgG := byte(bgColor >> 8)
	bgR := byte(bgColor)
	premulB := byte((uint16(bgB) * uint16(alpha)) / 255)
	premulG := byte((uint16(bgG) * uint16(alpha)) / 255)
	premulR := byte((uint16(bgR) * uint16(alpha)) / 255)

	for index := 0; index+3 < len(raw); index += 4 {
		raw[index] = premulB
		raw[index+1] = premulG
		raw[index+2] = premulR
		raw[index+3] = alpha
	}
}

func renderTextMask(width int, height int, font win.HFONT, text *uint16, rect *win.RECT, flags uint32) ([]byte, error) {
	hdc := win.CreateCompatibleDC(0)
	if hdc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer win.DeleteDC(hdc)

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
		return nil, fmt.Errorf("CreateDIBSection failed")
	}
	defer win.DeleteObject(win.HGDIOBJ(hbitmap))
	oldBitmap := win.SelectObject(hdc, win.HGDIOBJ(hbitmap))
	if oldBitmap == 0 {
		return nil, fmt.Errorf("SelectObject failed")
	}
	defer win.SelectObject(hdc, oldBitmap)
	oldFont := win.SelectObject(hdc, win.HGDIOBJ(font))
	if oldFont != 0 {
		defer win.SelectObject(hdc, oldFont)
	}
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.COLORREF(win.RGB(255, 255, 255)))
	maskRect := *rect
	textDraw(hdc, text, &maskRect, flags)
	mask := make([]byte, width*height*4)
	copy(mask, unsafe.Slice((*byte)(dibBits), width*height*4))
	return mask, nil
}

func blendTextMask(dst []byte, mask []byte, color uint32) {
	if len(dst) == 0 || len(mask) < len(dst) {
		return
	}
	srcB := byte(color >> 16)
	srcG := byte(color >> 8)
	srcR := byte(color)
	for index := 0; index+3 < len(dst); index += 4 {
		coverage := max(int(mask[index]), max(int(mask[index+1]), int(mask[index+2])))
		if coverage <= 0 {
			continue
		}
		blendPremultipliedPixel(dst[index:index+4], srcB, srcG, srcR, byte(coverage))
	}
}

func blendPremultipliedPixel(dst []byte, srcB byte, srcG byte, srcR byte, srcA byte) {
	if len(dst) < 4 || srcA == 0 {
		return
	}
	dstA := dst[3]
	oneMinusSrcA := 255 - int(srcA)
	srcPremulB := byte((int(srcB) * int(srcA)) / 255)
	srcPremulG := byte((int(srcG) * int(srcA)) / 255)
	srcPremulR := byte((int(srcR) * int(srcA)) / 255)
	dst[0] = byte(int(srcPremulB) + (int(dst[0])*oneMinusSrcA)/255)
	dst[1] = byte(int(srcPremulG) + (int(dst[1])*oneMinusSrcA)/255)
	dst[2] = byte(int(srcPremulR) + (int(dst[2])*oneMinusSrcA)/255)
	dst[3] = byte(int(srcA) + (int(dstA)*oneMinusSrcA)/255)
}

func createTextFont(family string, size int, bold bool, italic bool) (win.HFONT, error) {
	screenDC := win.GetDC(0)
	dpi := int32(96)
	if screenDC != 0 {
		dpi = win.GetDeviceCaps(screenDC, win.LOGPIXELSY)
		win.ReleaseDC(0, screenDC)
	}
	height := -win.MulDiv(int32(size), dpi, 72)
	weight := uintptr(400)
	if bold {
		weight = 700
	}
	italicFlag := uintptr(0)
	if italic {
		italicFlag = 1
	}
	if strings.TrimSpace(family) == "" {
		family = "Segoe UI"
	}
	faceName, _ := syscall.UTF16PtrFromString(family)
	hfont, _, _ := procTextCreateFontW.Call(
		uintptr(height),
		0,
		0,
		0,
		weight,
		italicFlag,
		0,
		0,
		uintptr(win.DEFAULT_CHARSET),
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(faceName)),
	)
	if hfont == 0 {
		return 0, fmt.Errorf("CreateFontW failed")
	}
	return win.HFONT(hfont), nil
}

func parseTextColor(value string, fallback uint32) uint32 {
	trimmed := strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if len(trimmed) != 6 {
		return fallback
	}
	parsed, err := strconv.ParseUint(trimmed, 16, 32)
	if err != nil {
		return fallback
	}
	r := byte(parsed >> 16)
	g := byte(parsed >> 8)
	b := byte(parsed)
	return uint32(win.RGB(r, g, b))
}

func textCreateSolidBrush(color uint32) win.HBRUSH {
	brush, _, _ := procTextCreateSolidBrush.Call(uintptr(color))
	return win.HBRUSH(brush)
}

func textFillRect(hdc win.HDC, rect *win.RECT, brush win.HBRUSH) {
	procTextFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}

func textDraw(hdc win.HDC, text *uint16, rect *win.RECT, format uint32) {
	procTextDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(rect)), uintptr(format))
}

func textDrawFlags(profile model.Profile) uint32 {
	flags := uint32(win.DT_WORDBREAK | win.DT_NOPREFIX)
	switch profile.TextAlignX {
	case model.TextAlignRight:
		flags |= win.DT_RIGHT
	case model.TextAlignCenter:
		flags |= win.DT_CENTER
	default:
		flags |= win.DT_LEFT
	}
	return flags
}