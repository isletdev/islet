//go:build windows

package files

import (
	"errors"
	"os"
)

func ownerOf(os.FileInfo) (string, string) { return "", "" }

// Chown is not supported on Windows; the panel is developed here but runs on Linux.
func Chown(string, string, string) error { return errors.New("chown is not supported on Windows") }
