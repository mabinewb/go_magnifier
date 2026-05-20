//go:build windows

package capture

import (
	"fmt"
	"image"
	"strings"
	"sync"

	dda "github.com/shinkar94/godesktopdup"
	"github.com/kbinani/screenshot"
)

const desktopDupTimeoutMs = 16
const desktopDupTransientErrorThreshold = 30

var (
	desktopDupSharedMu    sync.Mutex
	desktopDupSharedState *sharedDesktopDupState
)

type sharedDesktopDupState struct {
	mu       sync.Mutex
	refs     int
	capturer *desktopDupCapturer
}

type sharedDesktopDupCapturer struct {
	state *sharedDesktopDupState
}

type desktopDupCapturer struct {
	monitors []*desktopDupMonitor
}

type desktopDupMonitor struct {
	bounds   image.Rectangle
	dup      *dda.DesktopDuplication
	width    int
	height   int
	buffer   []byte
	hasFrame bool
	transientErrorCount int
	index    int
}

func newDesktopDupCapturer() (*sharedDesktopDupCapturer, error) {
	desktopDupSharedMu.Lock()
	defer desktopDupSharedMu.Unlock()
	if desktopDupSharedState == nil || desktopDupSharedState.capturer == nil {
		capturer, err := newOwnedDesktopDupCapturer()
		if err != nil {
			return nil, err
		}
		desktopDupSharedState = &sharedDesktopDupState{capturer: capturer}
	}
	desktopDupSharedState.refs++
	return &sharedDesktopDupCapturer{state: desktopDupSharedState}, nil
}

func newOwnedDesktopDupCapturer() (*desktopDupCapturer, error) {
	count := screenshot.NumActiveDisplays()
	if count <= 0 {
		return nil, fmt.Errorf("active display not found")
	}
	capturer := &desktopDupCapturer{monitors: make([]*desktopDupMonitor, 0, count)}
	for index := 0; index < count; index++ {
		dup, err := dda.New(uint(index))
		if err != nil {
			capturer.Dispose()
			return nil, fmt.Errorf("display %d Desktop Duplication 생성 실패: %w", index, err)
		}
		bounds := screenshot.GetDisplayBounds(index)
		dup.SetMonitorBounds(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)
		dup.SetCaptureCursor(false)
		width, height, err := dup.GetSize()
		if err != nil {
			dup.Release()
			capturer.Dispose()
			return nil, fmt.Errorf("display %d Desktop Duplication 크기 확인 실패: %w", index, err)
		}
		if width <= 0 || height <= 0 {
			width = bounds.Dx()
			height = bounds.Dy()
		}
		capturer.monitors = append(capturer.monitors, &desktopDupMonitor{
			bounds: bounds,
			dup:    dup,
			width:  width,
			height: height,
			buffer: make([]byte, width*height*4),
			index:  index,
		})
	}
	return capturer, nil
}

func (c *sharedDesktopDupCapturer) CaptureRect(sourceRect image.Rectangle) (*Frame, error) {
	if c == nil || c.state == nil {
		return nil, fmt.Errorf("desktop duplication capturer is not initialized")
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if c.state.capturer == nil {
		return nil, fmt.Errorf("desktop duplication capturer has been disposed")
	}
	return c.state.capturer.CaptureRect(sourceRect)
}

func (c *sharedDesktopDupCapturer) Dispose() {
	if c == nil || c.state == nil {
		return
	}
	state := c.state
	c.state = nil

	desktopDupSharedMu.Lock()
	if desktopDupSharedState == state && state.refs > 0 {
		state.refs--
		if state.refs == 0 {
			desktopDupSharedState = nil
			desktopDupSharedMu.Unlock()
			state.mu.Lock()
			if state.capturer != nil {
				state.capturer.Dispose()
				state.capturer = nil
			}
			state.mu.Unlock()
			return
		}
	}
	desktopDupSharedMu.Unlock()
}

func (c *desktopDupCapturer) CaptureRect(sourceRect image.Rectangle) (*Frame, error) {
	width := sourceRect.Dx()
	height := sourceRect.Dy()
	if width <= 0 || height <= 0 {
		return nil, nil
	}
	frame := &Frame{
		Pix:    make([]byte, width*height*4),
		Width:  width,
		Height: height,
		Stride: width * 4,
		Format: PixelFormatBGRA,
	}
	hit := false
	for _, monitor := range c.monitors {
		intersection := sourceRect.Intersect(monitor.bounds)
		if intersection.Empty() {
			continue
		}
		hit = true
		if err := monitor.updateFrame(); err != nil {
			return nil, err
		}
		copyMonitorRegion(frame, monitor, sourceRect, intersection)
	}
	if !hit {
		return nil, fmt.Errorf("capture rect is outside active displays")
	}
	return frame, nil
}

func (c *desktopDupCapturer) Dispose() {
	for _, monitor := range c.monitors {
		if monitor == nil || monitor.dup == nil {
			continue
		}
		monitor.dup.Release()
		monitor.dup = nil
		monitor.buffer = nil
		monitor.hasFrame = false
	}
	c.monitors = nil
}

func (m *desktopDupMonitor) updateFrame() error {
	if m == nil || m.dup == nil {
		return fmt.Errorf("desktop duplication monitor is not initialized")
	}
	frameBuffer := make([]byte, len(m.buffer))
	err := m.dup.GetFrameBGRA(frameBuffer, desktopDupTimeoutMs)
	if err == nil {
		m.buffer = frameBuffer
		m.hasFrame = true
		m.transientErrorCount = 0
		return nil
	}
	message := strings.ToLower(err.Error())
	if (strings.Contains(message, "no image yet") || strings.Contains(message, "timeout waiting for frame")) && m.hasFrame {
		return nil
	}
	if strings.Contains(message, "no image yet") || strings.Contains(message, "timeout waiting for frame") {
		m.transientErrorCount++
		if m.transientErrorCount <= desktopDupTransientErrorThreshold {
			return nil
		}
	} else {
		m.transientErrorCount = 0
	}
	return fmt.Errorf("display %d Desktop Duplication 캡처 실패: %w", m.index, err)
}

func copyMonitorRegion(dst *Frame, monitor *desktopDupMonitor, sourceRect image.Rectangle, region image.Rectangle) {
	srcStartX := region.Min.X - monitor.bounds.Min.X
	srcStartY := region.Min.Y - monitor.bounds.Min.Y
	dstStartX := region.Min.X - sourceRect.Min.X
	dstStartY := region.Min.Y - sourceRect.Min.Y
	rowBytes := region.Dx() * 4
	for row := 0; row < region.Dy(); row++ {
		srcOffset := ((srcStartY + row) * monitor.width + srcStartX) * 4
		dstOffset := ((dstStartY + row) * dst.Width + dstStartX) * 4
		copy(dst.Pix[dstOffset:dstOffset+rowBytes], monitor.buffer[srcOffset:srcOffset+rowBytes])
	}
}