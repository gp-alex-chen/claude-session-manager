//go:build windows && integration

package terminal

import (
	"github.com/UserExistsError/conpty"
	"testing"
	"time"
)

func TestPtyRefNoDoubleClose(t *testing.T) {
	p, err := conpty.Start(`cmd /c echo INTEGRATION & ping -n 5 127.0.0.1 >nul`, conpty.ConPtyDimensions(120, 32), conpty.ConPtyWorkDir(`C:\`))
	if err != nil {
		t.Fatal(err)
	}
	r := &ptyRef{pty: p}
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 8192)
		for {
			if _, err := r.pty.Read(buf); err != nil {
				break
			}
		}
		r.Close()
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	r.Close()
	r.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not exit after idempotent close")
	}
}
