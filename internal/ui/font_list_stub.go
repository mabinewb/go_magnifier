//go:build !windows

package ui

func loadSystemFontOptions() []string {
	return fallbackFontOptions()
}

func fallbackFontOptions() []string {
	return []string{"Segoe UI", "Malgun Gothic", "Arial", "Consolas", "Times New Roman"}
}
