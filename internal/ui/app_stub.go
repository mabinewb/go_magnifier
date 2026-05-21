//go:build !windows

package ui

import "fmt"

const appName = "GoMagnifier"

func Run() error {
	return fmt.Errorf("GoMagnifier is only supported on Windows")
}
