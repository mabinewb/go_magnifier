//go:build windows

package capture

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/lxn/win"

	"gomagnifier/internal/model"
)

type screenCapturer struct {
	srcDC     win.HDC
	memDC     win.HDC
	dibBitmap win.HBITMAP
	oldBitmap win.HGDIOBJ
	dibBits   unsafe.Pointer
	raw       []byte
	width     int
	height    int
}

func newScreenCapturer() *screenCapturer {
	return &screenCapturer{}
}

func newScreenCapturerForBackend(backend string) (screenCapturerBackend, string, error) {
	switch backend {
	case model.CaptureBackendDDA:
		capturer, err := newDesktopDupCapturer()
		if err != nil {
			return newScreenCapturer(), model.CaptureBackendGDI, fmt.Errorf("Desktop Duplication 초기화 실패, 기존 GDI BitBlt로 대체합니다: %w", err)
		}
		return capturer, model.CaptureBackendDDA, nil
	case model.CaptureBackendGDI:
		return newScreenCapturer(), model.CaptureBackendGDI, nil
	default:
		capturer, err := newDesktopDupCapturer()
		if err != nil {
			return newScreenCapturer(), model.CaptureBackendGDI, fmt.Errorf("자동 백엔드에서 Desktop Duplication 초기화 실패, 기존 GDI BitBlt로 대체합니다: %w", err)
		}
		return capturer, model.CaptureBackendDDA, nil
	}
}

func (c *screenCapturer) CaptureRect(sourceRect image.Rectangle) (*Frame, error) {
	width := sourceRect.Dx()
	height := sourceRect.Dy()
	if width <= 0 || height <= 0 {
		return nil, nil
	}
	if err := c.ensureResources(width, height); err != nil {
		return nil, err
	}

	if !win.BitBlt(
		c.memDC,
		0,
		0,
		int32(width),
		int32(height),
		c.srcDC,
		int32(sourceRect.Min.X),
		int32(sourceRect.Min.Y),
		win.SRCCOPY,
	) {
		return nil, fmt.Errorf("BitBlt failed")
	}

	frame := cloneBGRAFrame(c.raw, width, height)
	return frame, nil
}

func (c *screenCapturer) ensureResources(width, height int) error {
	if c.srcDC == 0 {
		c.srcDC = win.GetDC(0)
		if c.srcDC == 0 {
			return fmt.Errorf("GetDC failed")
		}
	}
	if c.memDC == 0 {
		c.memDC = win.CreateCompatibleDC(c.srcDC)
		if c.memDC == 0 {
			return fmt.Errorf("CreateCompatibleDC failed")
		}
	}
	if c.dibBits != nil && c.width == width && c.height == height {
		return nil
	}
	c.disposeBitmap()

	bitmapHeader := win.BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(win.BITMAPINFOHEADER{})),
		BiWidth:       int32(width),
		BiHeight:      -int32(height),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: win.BI_RGB,
	}

	var dibBits unsafe.Pointer
	dibBitmap := win.CreateDIBSection(c.srcDC, &bitmapHeader, win.DIB_RGB_COLORS, &dibBits, 0, 0)
	if dibBitmap == 0 || dibBits == nil {
		return fmt.Errorf("CreateDIBSection failed")
	}
	oldBitmap := win.SelectObject(c.memDC, win.HGDIOBJ(dibBitmap))
	if oldBitmap == 0 {
		win.DeleteObject(win.HGDIOBJ(dibBitmap))
		return fmt.Errorf("SelectObject failed")
	}
	c.dibBitmap = dibBitmap
	c.oldBitmap = oldBitmap
	c.dibBits = dibBits
	c.width = width
	c.height = height
	c.raw = unsafe.Slice((*byte)(dibBits), width*height*4)
	return nil
}

func (c *screenCapturer) disposeBitmap() {
	if c.memDC != 0 && c.oldBitmap != 0 {
		win.SelectObject(c.memDC, c.oldBitmap)
		c.oldBitmap = 0
	}
	if c.dibBitmap != 0 {
		win.DeleteObject(win.HGDIOBJ(c.dibBitmap))
		c.dibBitmap = 0
	}
	c.dibBits = nil
	c.raw = nil
	c.width = 0
	c.height = 0
}

func (c *screenCapturer) Dispose() {
	c.disposeBitmap()
	if c.memDC != 0 {
		win.DeleteDC(c.memDC)
		c.memDC = 0
	}
	if c.srcDC != 0 {
		win.ReleaseDC(0, c.srcDC)
		c.srcDC = 0
	}
}

func cloneBGRAFrame(raw []byte, width, height int) *Frame {
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return &Frame{Pix: cloned, Width: width, Height: height, Stride: width * 4, Format: PixelFormatBGRA}
}

func convertBGRAToRGBA(raw []byte, width, height int) *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	copyBGRAToRGBA(frame, raw)
	return frame
}

func convertRGBAToFrame(src *image.RGBA) *Frame {
	if src == nil {
		return nil
	}
	width := src.Rect.Dx()
	height := src.Rect.Dy()
	pix := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		srcRow := src.Pix[y*src.Stride : y*src.Stride+width*4]
		dstRow := pix[y*width*4 : (y+1)*width*4]
		for offset := 0; offset < len(srcRow); offset += 4 {
			dstRow[offset] = srcRow[offset+2]
			dstRow[offset+1] = srcRow[offset+1]
			dstRow[offset+2] = srcRow[offset]
			dstRow[offset+3] = srcRow[offset+3]
		}
	}
	return &Frame{Pix: pix, Width: width, Height: height, Stride: width * 4, Format: PixelFormatBGRA}
}

func copyBGRAToRGBA(dst *image.RGBA, raw []byte) {
	if dst == nil {
		return
	}
	for offset := 0; offset < len(raw); offset += 4 {
		dst.Pix[offset+0] = raw[offset+2]
		dst.Pix[offset+1] = raw[offset+1]
		dst.Pix[offset+2] = raw[offset+0]
		dst.Pix[offset+3] = 0xFF
	}
}
