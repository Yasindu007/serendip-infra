//go:build !windows

package main

import "os"

// makePrivate makes dir accessible to its owner alone, even when it
// already existed with wider permissions (MkdirAll doesn't tighten).
func makePrivate(dir string) error {
	return os.Chmod(dir, 0o700)
}
