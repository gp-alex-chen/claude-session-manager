//go:build windows

package provider

import (
	"golang.org/x/sys/windows"
	"os"
)

// The caller holds the separate snapshot lock until this reader is closed.
// Delete sharing alone is insufficient for replacement on some Windows builds.
func openAntigravitySnapshot(path string) (*os.File, error) {
	return os.Open(path)
}

func lockAntigravitySnapshot(path string, wait bool) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	overlap := new(windows.Overlapped)
	if err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, overlap); err != nil {
		f.Close()
		if err == windows.ERROR_LOCK_VIOLATION {
			return nil, errAntigravitySnapshotBusy
		}
		return nil, err
	}
	return func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlap); f.Close() }, nil
}
