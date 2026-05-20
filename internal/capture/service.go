package capture

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gomagnifier/internal/model"
)

type PixelFormat uint8

const (
	PixelFormatBGRA PixelFormat = iota + 1
	PixelFormatRGBA
)

type Frame struct {
	Pix    []byte
	Width  int
	Height int
	Stride int
	Format PixelFormat
}

type Service struct {
	onFrame func(*Frame)
	onError func(error)
	onBackendChanged func(string)

	mu      sync.RWMutex
	profile model.Profile
	cancel  context.CancelFunc
	running bool
	cache   imageCache
	updateCh chan struct{}
}

type imageCache struct {
	path    string
	modTime time.Time
	frame   *Frame
	animatedFrames []*Frame
	animatedDelays []time.Duration
	animatedIndex  int
}

type screenCapturerBackend interface {
	CaptureRect(sourceRect image.Rectangle) (*Frame, error)
	Dispose()
}

func NewService(onFrame func(*Frame), onError func(error), onBackendChanged func(string)) *Service {
	return &Service{
		onFrame: onFrame,
		onError: onError,
		onBackendChanged: onBackendChanged,
		updateCh: make(chan struct{}, 1),
	}
}

func (s *Service) Start(profile model.Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.profile = profile.Sanitized()
		s.cache = imageCache{}
		s.signalUpdateLocked()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.profile = profile.Sanitized()
	s.running = true
	s.cache = imageCache{}
	s.signalUpdateLocked()

	go s.loop(ctx)
}

func (s *Service) UpdateProfile(profile model.Profile) {
	s.mu.Lock()
	s.profile = profile.Sanitized()
	s.cache = imageCache{}
	if s.running {
		s.signalUpdateLocked()
	}
	s.mu.Unlock()
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *Service) loop(ctx context.Context) {
	var capturer screenCapturerBackend
	currentBackendSetting := ""
	currentActualBackend := ""
	defer func() {
		if capturer != nil {
			capturer.Dispose()
		}
	}()

	for {
		profile := s.currentProfile()
		desiredBackend := normalizedScreenCaptureBackend(profile)
		if desiredBackend != currentBackendSetting || capturer == nil {
			if capturer != nil {
				capturer.Dispose()
			}
			created, actualBackend, err := newScreenCapturerForBackend(desiredBackend)
			if err != nil && s.onError != nil {
				s.onError(err)
			}
			capturer = created
			currentBackendSetting = desiredBackend
			if actualBackend != currentActualBackend {
				currentActualBackend = actualBackend
				if s.onBackendChanged != nil {
					s.onBackendChanged(actualBackend)
				}
			}
		}
		if profile.SourceKind == model.SourceScreen {
			timer := time.NewTimer(time.Second / time.Duration(profile.RefreshRate))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-s.updateCh:
				timer.Stop()
				continue
			case <-timer.C:
			}
			if shouldSuspendCapture() {
				continue
			}
			frame, _, err := s.captureFrame(profile, capturer)
			if err != nil {
				if s.onError != nil {
					s.onError(err)
				}
				continue
			}
			if frame != nil && s.onFrame != nil {
				s.onFrame(frame)
			}
			continue
		}

		frame, delay, err := s.captureFrame(profile, capturer)
		if err != nil {
			if s.onError != nil {
				s.onError(err)
			}
		} else if frame != nil && s.onFrame != nil {
			s.onFrame(frame)
		}

		if delay <= 0 {
			select {
			case <-ctx.Done():
				return
			case <-s.updateCh:
			}
			continue
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.updateCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Service) signalUpdateLocked() {
	select {
	case s.updateCh <- struct{}{}:
	default:
	}
}

func (s *Service) currentProfile() model.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profile
}

func (s *Service) captureFrame(profile model.Profile, capturer screenCapturerBackend) (*Frame, time.Duration, error) {
	if profile.SourceKind == model.SourceImage {
		return s.loadImageFrame(profile)
	}
	if profile.SourceKind == model.SourceText {
		frame, err := renderTextFrame(profile)
		return frame, 0, err
	}

	sourceRect, err := effectiveCaptureRect(profile)
	if err != nil {
		return nil, 0, err
	}
	if sourceRect.Empty() {
		return nil, 0, nil
	}
	img, err := capturer.CaptureRect(sourceRect)
	if err != nil {
		return nil, 0, fmt.Errorf("capture failed: %w", err)
	}
	return img, 0, nil
}

func (s *Service) loadImageFrame(profile model.Profile) (*Frame, time.Duration, error) {
	if profile.ImagePath == "" {
		return nil, 0, nil
	}

	fileInfo, err := os.Stat(profile.ImagePath)
	if err != nil {
		return nil, 0, fmt.Errorf("image file unavailable: %w", err)
	}

	s.mu.RLock()
	cache := s.cache
	s.mu.RUnlock()
	if cache.frame != nil && cache.path == profile.ImagePath && cache.modTime.Equal(fileInfo.ModTime()) {
		return cache.frame, 0, nil
	}
	if len(cache.animatedFrames) > 0 && cache.path == profile.ImagePath && cache.modTime.Equal(fileInfo.ModTime()) {
		index := cache.animatedIndex % len(cache.animatedFrames)
		frame := cache.animatedFrames[index]
		delay := cache.animatedDelays[index]
		s.mu.Lock()
		cache = s.cache
		cache.animatedIndex = (index + 1) % len(cache.animatedFrames)
		s.cache = cache
		s.mu.Unlock()
		return frame, delay, nil
	}

	data, err := os.ReadFile(profile.ImagePath)
	if err != nil {
		return nil, 0, fmt.Errorf("image open failed: %w", err)
	}
	if strings.EqualFold(filepath.Ext(profile.ImagePath), ".gif") {
		decodedGIF, err := gif.DecodeAll(bytes.NewReader(data))
		if err == nil && decodedGIF != nil && len(decodedGIF.Image) > 1 {
			frames, delays := buildGIFFrames(decodedGIF)
			if len(frames) > 0 {
				firstDelay := delays[0]
				s.mu.Lock()
				s.cache = imageCache{path: profile.ImagePath, modTime: fileInfo.ModTime(), animatedFrames: frames, animatedDelays: delays, animatedIndex: 1 % len(frames)}
				s.mu.Unlock()
				return frames[0], firstDelay, nil
			}
		}
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("image decode failed: %w", err)
	}

	rgba := image.NewRGBA(decoded.Bounds())
	draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	bgra := convertRGBAToFrame(rgba)

	s.mu.Lock()
	s.cache = imageCache{path: profile.ImagePath, modTime: fileInfo.ModTime(), frame: bgra}
	s.mu.Unlock()
	return bgra, 0, nil
}

func buildGIFFrames(decoded *gif.GIF) ([]*Frame, []time.Duration) {
	if decoded == nil || len(decoded.Image) == 0 {
		return nil, nil
	}
	canvas := image.NewRGBA(image.Rect(0, 0, decoded.Config.Width, decoded.Config.Height))
	frames := make([]*Frame, 0, len(decoded.Image))
	delays := make([]time.Duration, 0, len(decoded.Image))
	for index, src := range decoded.Image {
		draw.Draw(canvas, src.Bounds(), src, src.Bounds().Min, draw.Over)
		snapshot := image.NewRGBA(canvas.Bounds())
		draw.Draw(snapshot, snapshot.Bounds(), canvas, canvas.Bounds().Min, draw.Src)
		frames = append(frames, convertRGBAToFrame(snapshot))
		delay := 100 * time.Millisecond
		if index < len(decoded.Delay) && decoded.Delay[index] > 0 {
			delay = time.Duration(decoded.Delay[index]) * 10 * time.Millisecond
		}
		delays = append(delays, delay)
	}
	return frames, delays
}

func effectiveCaptureRect(profile model.Profile) (image.Rectangle, error) {
	rect := profile.EffectiveCaptureRect()
	if rect.Empty() {
		return image.Rectangle{}, nil
	}
	return rect, nil
}

func normalizedScreenCaptureBackend(profile model.Profile) string {
	switch profile.CaptureBackend {
	case model.CaptureBackendDDA:
		return model.CaptureBackendDDA
	case model.CaptureBackendGDI:
		return model.CaptureBackendGDI
	default:
		return model.CaptureBackendAuto
	}
}
