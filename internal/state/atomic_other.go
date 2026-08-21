//go:build !windows

package state

import "os"

func atomicReplace(from, to string) error { return os.Rename(from, to) }
