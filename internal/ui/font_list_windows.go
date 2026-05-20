//go:build windows

package ui

import (
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

var (
	fontGdi32                 = syscall.NewLazyDLL("gdi32.dll")
	procEnumFontFamiliesExW   = fontGdi32.NewProc("EnumFontFamiliesExW")
)

func loadSystemFontOptions() []string {
	hdc := win.GetDC(0)
	if hdc == 0 {
		return fallbackFontOptions()
	}
	defer win.ReleaseDC(0, hdc)

	seen := map[string]struct{}{}
	fonts := make([]string, 0, 64)
	var logFont win.LOGFONT
	logFont.LfCharSet = byte(win.DEFAULT_CHARSET)

	callback := syscall.NewCallback(func(lpelfe, _lpntme, _fontType, lParam uintptr) uintptr {
		face := (*win.LOGFONT)(unsafe.Pointer(lpelfe))
		name := syscall.UTF16ToString(face.LfFaceName[:])
		name = strings.TrimSpace(name)
		if name == "" {
			return 1
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			return 1
		}
		seen[strings.ToLower(name)] = struct{}{}
		fonts = append(fonts, name)
		return 1
	})

	procEnumFontFamiliesExW.Call(
		uintptr(hdc),
		uintptr(unsafe.Pointer(&logFont)),
		callback,
		0,
		0,
	)

	if len(fonts) == 0 {
		return fallbackFontOptions()
	}
	sort.Strings(fonts)
	return fonts
}

func fallbackFontOptions() []string {
	return []string{"Segoe UI", "Malgun Gothic", "Arial", "Consolas", "Times New Roman"}
}
