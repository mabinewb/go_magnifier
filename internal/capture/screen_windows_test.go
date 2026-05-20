//go:build windows

package capture

import (
	"image"
	"testing"
)

func BenchmarkConvertBGRAToRGBA_640x360(b *testing.B) {
	benchmarkConvertBGRAToRGBA(b, 640, 360)
}

func BenchmarkConvertBGRAToRGBA_1280x720(b *testing.B) {
	benchmarkConvertBGRAToRGBA(b, 1280, 720)
}

func BenchmarkCopyBGRAToRGBA_Reuse_1280x720(b *testing.B) {
	width := 1280
	height := 720
	raw := make([]byte, width*height*4)
	for index := range raw {
		raw[index] = byte(index)
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		copyBGRAToRGBA(dst, raw)
	}
}

func BenchmarkCloneBGRAFrame_1280x720(b *testing.B) {
	width := 1280
	height := 720
	raw := make([]byte, width*height*4)
	for index := range raw {
		raw[index] = byte(index)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		frame := cloneBGRAFrame(raw, width, height)
		if frame.Width != width || frame.Height != height {
			b.Fatalf("unexpected frame size: %dx%d", frame.Width, frame.Height)
		}
	}
}

func BenchmarkConvertRGBAToFrame_1280x720(b *testing.B) {
	width := 1280
	height := 720
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for index := range rgba.Pix {
		rgba.Pix[index] = byte(index)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(rgba.Pix)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		frame := convertRGBAToFrame(rgba)
		if frame.Width != width || frame.Height != height {
			b.Fatalf("unexpected frame size: %dx%d", frame.Width, frame.Height)
		}
	}
}

func BenchmarkScreenCapturer_Fresh_320x180(b *testing.B) {
	rect := image.Rect(0, 0, 320, 180)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		capturer := newScreenCapturer()
		frame, err := capturer.CaptureRect(rect)
		capturer.Dispose()
		if err != nil {
			b.Fatal(err)
		}
		if frame == nil {
			b.Fatal("expected frame")
		}
	}
}

func BenchmarkScreenCapturer_Reuse_320x180(b *testing.B) {
	rect := image.Rect(0, 0, 320, 180)
	capturer := newScreenCapturer()
	defer capturer.Dispose()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		frame, err := capturer.CaptureRect(rect)
		if err != nil {
			b.Fatal(err)
		}
		if frame == nil {
			b.Fatal("expected frame")
		}
	}
}

func benchmarkConvertBGRAToRGBA(b *testing.B, width, height int) {
	raw := make([]byte, width*height*4)
	for index := range raw {
		raw[index] = byte(index)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		frame := convertBGRAToRGBA(raw, width, height)
		if frame.Rect.Dx() != width || frame.Rect.Dy() != height {
			b.Fatalf("unexpected frame size: %v", frame.Rect)
		}
	}
}