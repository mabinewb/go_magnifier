//go:build windows

package ui

import (
	"os"
	"path/filepath"

	"github.com/lxn/walk"
)

func loadShellAppIcon() (*walk.Icon, error) {
	if iconPath := findAppIconPath(); iconPath != "" {
		icon, err := walk.NewIconFromFile(iconPath)
		if err == nil {
			return icon, nil
		}
	}

	return walk.NewIconFromSysDLLWithSize("shell32", 22, 16)
}

func findAppIconPath() string {
	if exePath, err := os.Executable(); err == nil {
		iconPath := filepath.Join(filepath.Dir(exePath), "GoMagnifier.ico")
		if fileExists(iconPath) {
			return iconPath
		}
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return ""
	}

	candidates := []string{
		filepath.Join(workingDir, "packaging", "GoMagnifier.ico"),
		filepath.Join(workingDir, "dist", "GoMagnifier.ico"),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}