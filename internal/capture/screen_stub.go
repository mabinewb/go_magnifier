//go:build !windows

package capture

import (
	"fmt"
	"image"

	"gomagnifier/internal/model"
)

type unsupportedScreenCapturer struct{}

func (unsupportedScreenCapturer) CaptureRect(sourceRect image.Rectangle) (*Frame, error) {
	return nil, fmt.Errorf("screen capture is only supported on Windows")
}

func (unsupportedScreenCapturer) Dispose() {}

func newScreenCapturerForBackend(_ string) (screenCapturerBackend, string, error) {
	return unsupportedScreenCapturer{}, model.CaptureBackendGDI, fmt.Errorf("screen capture is only supported on Windows")
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
