//go:build !windows

package capture

import (
	"fmt"

	"gomagnifier/internal/model"
)

func renderTextFrame(profile model.Profile) (*Frame, error) {
	return nil, fmt.Errorf("text rendering is only supported on Windows")
}