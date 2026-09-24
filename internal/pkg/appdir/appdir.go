// Package appdir names the directory ohmyssh keeps its files in.
//
// It exists so that the saved passwords, the hosts added from the browser and
// anything else ohmyssh writes down live in one place that is defined once. The
// arrangement is the same on every system ohmyssh builds for: a dot directory in
// the home directory os.UserHomeDir reports, which is %USERPROFILE% on Windows
// and $HOME elsewhere.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Name is the directory, under the user's home directory.
const Name = ".ohmyssh"

// Dir is the directory ohmyssh keeps this user's files in. It is reported
// whether or not it exists; the files under it are created by whoever writes
// them, at the mode that suits them.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, Name), nil
}

// Path is the path of one of ohmyssh's files, by name.
func Path(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
