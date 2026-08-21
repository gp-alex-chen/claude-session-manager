//go:build windows

package notify

import (
	"syscall"
	"time"
)

func Beep() {
	go func() {
		user32 := syscall.NewLazyDLL("user32.dll")
		user32.NewProc("MessageBeep").Call(0x40)
		k32 := syscall.NewLazyDLL("kernel32.dll")
		beep := k32.NewProc("Beep")
		for _, f := range []int{880, 1108, 1319} {
			beep.Call(uintptr(f), 140)
			time.Sleep(30 * time.Millisecond)
		}
	}()
}
