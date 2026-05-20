package model

import "image"

const (
	MinZoom        = 0.10
	MaxZoom        = 800.0
	MinOverlaySize = 1
	SourceScreen   = "screen"
	SourceImage    = "image"
	SourceText     = "text"
	CaptureBackendAuto = "auto"
	CaptureBackendGDI = "gdi"
	CaptureBackendDDA = "dda"
	TextAlignLeft  = "left"
	TextAlignCenter = "center"
	TextAlignRight = "right"
	TextAlignTop   = "top"
	TextAlignMiddle = "middle"
	TextAlignBottom = "bottom"
)

type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func RectFromImage(rect image.Rectangle) Rect {
	return Rect{
		X:      rect.Min.X,
		Y:      rect.Min.Y,
		Width:  rect.Dx(),
		Height: rect.Dy(),
	}
}

func (r Rect) Empty() bool {
	return r.Width <= 0 || r.Height <= 0
}

func (r Rect) ImageRect() image.Rectangle {
	return image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height)
}

type Profile struct {
	ID           string  `json:"id,omitempty"`
	Name         string  `json:"name,omitempty"`
	SourceKind   string  `json:"sourceKind,omitempty"`
	ImagePath    string  `json:"imagePath,omitempty"`
	TextContent  string  `json:"textContent,omitempty"`
	TextFontSize int     `json:"textFontSize,omitempty"`
	TextFontFamily string `json:"textFontFamily,omitempty"`
	TextColor    string  `json:"textColor,omitempty"`
	TextBackground string `json:"textBackground,omitempty"`
	TextBackgroundAlpha int `json:"textBackgroundAlpha,omitempty"`
	TextTransparentBackground bool `json:"textTransparentBackground,omitempty"`
	TextBold     bool    `json:"textBold,omitempty"`
	TextItalic   bool    `json:"textItalic,omitempty"`
	TextShadow   bool    `json:"textShadow,omitempty"`
	TextShadowColor string `json:"textShadowColor,omitempty"`
	TextShadowOffset int `json:"textShadowOffset,omitempty"`
	TextAlignX   string  `json:"textAlignX,omitempty"`
	TextAlignY   string  `json:"textAlignY,omitempty"`
	CaptureRect  Rect    `json:"captureRect"`
	OverlayRect  Rect    `json:"overlayRect"`
	Opacity      int     `json:"opacity"`
	Zoom         float64 `json:"zoom,omitempty"`
	ZoomX        float64 `json:"zoomX,omitempty"`
	ZoomY        float64 `json:"zoomY,omitempty"`
	RefreshRate  int     `json:"refreshRate"`
	CaptureBackend string `json:"captureBackend,omitempty"`
	ClickThrough bool    `json:"clickThrough"`
	Disabled     bool    `json:"disabled,omitempty"`
	LockAspect   bool    `json:"lockAspect"`
	BlockRecursiveCapture bool `json:"blockRecursiveCapture,omitempty"`
}

type Session struct {
	ActiveOverlayID string    `json:"activeOverlayId,omitempty"`
	MainWindowRect  Rect      `json:"mainWindowRect,omitempty"`
	GlobalClickThrough bool   `json:"globalClickThrough,omitempty"`
	OverlaysGloballyDisabled bool `json:"overlaysGloballyDisabled,omitempty"`
	MinimizeToTray bool       `json:"minimizeToTray,omitempty"`
	CloseToTrayOnClose bool   `json:"closeToTrayOnClose,omitempty"`
	DisableUpdateCheck bool   `json:"disableUpdateCheck,omitempty"`
	SkippedUpdateVersion string `json:"skippedUpdateVersion,omitempty"`
	AlwaysOnTop    bool       `json:"alwaysOnTop,omitempty"`
	Overlays        []Profile `json:"overlays"`
}

type State struct {
	AutoLoadLast bool   `json:"autoLoadLast"`
	LastPreset   string `json:"lastPreset"`
}

func DefaultProfile(bounds image.Rectangle) Profile {
	width := max(320, bounds.Dx()/5)
	height := max(180, bounds.Dy()/5)
	overlayWidth := width
	overlayHeight := height

	return Profile{
		SourceKind: SourceScreen,
		CaptureRect: Rect{
			X:      bounds.Min.X + 48,
			Y:      bounds.Min.Y + 48,
			Width:  width,
			Height: height,
		},
		OverlayRect: Rect{
			X:      bounds.Min.X + bounds.Dx() - overlayWidth - 48,
			Y:      bounds.Min.Y + 48,
			Width:  overlayWidth,
			Height: overlayHeight,
		},
		Opacity:      75,
		Zoom:         100,
		ZoomX:        100,
		ZoomY:        100,
		RefreshRate:  60,
		CaptureBackend: CaptureBackendAuto,
		TextContent:  "텍스트 오버레이",
		TextFontSize: 36,
		TextFontFamily: "Segoe UI",
		TextColor:    "#F0F8FF",
		TextBackground: "#102030",
		TextBackgroundAlpha: 100,
		TextTransparentBackground: false,
		TextShadow:   false,
		TextShadowColor: "#000000",
		TextShadowOffset: 2,
		TextAlignX:   TextAlignCenter,
		TextAlignY:   TextAlignMiddle,
		ClickThrough: false,
		LockAspect:   true,
		BlockRecursiveCapture: false,
	}
}

func (p Profile) Sanitized() Profile {
	if p.TextFontSize <= 0 {
		p.TextFontSize = 36
	}
	p.TextFontSize = clamp(p.TextFontSize, 10, 144)
	if p.TextFontFamily == "" {
		p.TextFontFamily = "Segoe UI"
	}
	if p.TextColor == "" {
		p.TextColor = "#F0F8FF"
	}
	if p.TextBackground == "" {
		p.TextBackground = "#102030"
	}
	p.TextBackgroundAlpha = clamp(p.TextBackgroundAlpha, 0, 100)
	p.TextTransparentBackground = false
	if p.TextShadowColor == "" {
		p.TextShadowColor = "#000000"
	}
	p.TextShadowOffset = clamp(p.TextShadowOffset, 0, 24)
	switch p.TextAlignX {
	case TextAlignLeft, TextAlignCenter, TextAlignRight:
	default:
		p.TextAlignX = TextAlignCenter
	}
	switch p.TextAlignY {
	case TextAlignTop, TextAlignMiddle, TextAlignBottom:
	default:
		p.TextAlignY = TextAlignMiddle
	}
	p.Opacity = clamp(p.Opacity, 0, 100)
	legacyZoom := clampFloat(p.Zoom, MinZoom, MaxZoom)
	if p.ZoomX == 0 {
		if !p.CaptureRect.Empty() && !p.OverlayRect.Empty() {
			p.ZoomX = zoomPercent(p.OverlayRect.Width, p.CaptureRect.Width)
		} else {
			p.ZoomX = legacyZoom
		}
	}
	if p.ZoomY == 0 {
		if !p.CaptureRect.Empty() && !p.OverlayRect.Empty() {
			p.ZoomY = zoomPercent(p.OverlayRect.Height, p.CaptureRect.Height)
		} else {
			p.ZoomY = legacyZoom
		}
	}
	p.ZoomX = clampFloat(p.ZoomX, MinZoom, MaxZoom)
	p.ZoomY = clampFloat(p.ZoomY, MinZoom, MaxZoom)
	p.Zoom = 0
	if p.SourceKind != SourceImage && p.SourceKind != SourceText {
		p.SourceKind = SourceScreen
	}
	if p.SourceKind != SourceImage {
		p.ImagePath = ""
	}
	p.RefreshRate = clamp(p.RefreshRate, 15, 60)
	switch p.CaptureBackend {
	case CaptureBackendAuto, CaptureBackendGDI, CaptureBackendDDA:
	default:
		p.CaptureBackend = CaptureBackendAuto
	}
	if p.CaptureRect.Width < 1 {
		p.CaptureRect.Width = 1
	}
	if p.CaptureRect.Height < 1 {
		p.CaptureRect.Height = 1
	}
	if p.OverlayRect.Width < MinOverlaySize {
		p.OverlayRect.Width = MinOverlaySize
	}
	if p.OverlayRect.Height < MinOverlaySize {
		p.OverlayRect.Height = MinOverlaySize
	}
	if p.LockAspect && p.SourceKind != SourceText {
		p.OverlayRect = p.FittedOverlayRect(p.OverlayRect)
	}
	return p
}

func (p Profile) EffectiveCaptureRect() image.Rectangle {
	if p.SourceKind == SourceImage || p.SourceKind == SourceText {
		return image.Rect(0, 0, p.CaptureRect.Width, p.CaptureRect.Height)
	}
	return p.CaptureRect.ImageRect()
}

func (p Profile) EffectiveAspectRatio() float64 {
	rect := p.CaptureRect.ImageRect()
	if rect.Empty() || rect.Dy() == 0 {
		return 1
	}
	return float64(rect.Dx()) / float64(rect.Dy())
}

func (p Profile) FittedOverlayRect(rect Rect) Rect {
	if rect.Empty() {
		return rect
	}
	ratio := p.EffectiveAspectRatio()
	if ratio <= 0 {
		return rect
	}
	width := rect.Width
	height := int(float64(width) / ratio)
	if height < MinOverlaySize {
		height = MinOverlaySize
		width = int(float64(height) * ratio)
	}
	rect.Width = max(MinOverlaySize, width)
	rect.Height = max(MinOverlaySize, height)
	return rect
}

func zoomPercent(overlaySize, captureSize int) float64 {
	if overlaySize <= 0 || captureSize <= 0 {
		return 100
	}
	return clampFloat((float64(overlaySize)*100)/float64(captureSize), MinZoom, MaxZoom)
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}