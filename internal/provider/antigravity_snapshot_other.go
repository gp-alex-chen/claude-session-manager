//go:build !windows

package provider

import (
	"os"
	"sync"
)

func openAntigravitySnapshot(path string) (*os.File, error) { return os.Open(path) }

var antigravitySnapshotLocks sync.Map

func lockAntigravitySnapshot(path string, wait bool) (func(), error) {
	value, _ := antigravitySnapshotLocks.LoadOrStore(path, new(sync.Mutex))
	mu := value.(*sync.Mutex)
	if wait {
		mu.Lock()
	} else if !mu.TryLock() {
		return nil, errAntigravitySnapshotBusy
	}
	return mu.Unlock, nil
}
