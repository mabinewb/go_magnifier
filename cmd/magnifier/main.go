//go:build windows

package main

import (
	"log"

	"gomagnifier/internal/ui"
	"gomagnifier/internal/version"
)

func main() {
	log.SetPrefix(version.AppTitle() + ": ")
	if err := ui.Run(); err != nil {
		log.Fatal(err)
	}
}
