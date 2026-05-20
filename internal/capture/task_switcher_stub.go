//go:build !windows

package capture

func shouldSuspendCapture() bool {
	return false
}