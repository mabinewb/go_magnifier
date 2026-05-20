package capture

import "gomagnifier/internal/model"

func RenderTextFrame(profile model.Profile) (*Frame, error) {
	return renderTextFrame(profile.Sanitized())
}