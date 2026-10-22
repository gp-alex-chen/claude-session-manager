//go:build windows && integration

package notify

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Creates only a hidden, process-owned fixture window, never a real app/profile.
func TestTaskbarBadgeNativeIconCOMAndResourcesIntegration(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	kernel := syscall.NewLazyDLL("kernel32.dll")
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	class, _ := syscall.UTF16PtrFromString(WindowClassName)
	proc := badgeUser32.NewProc("DefWindowProcW")
	proc.Find()
	type windowClass struct {
		Size, Style                        uint32
		WndProc                            uintptr
		ClassExtra, WindowExtra            int32
		Instance, Icon, Cursor, Background uintptr
		MenuName, ClassName                *uint16
		SmallIcon                          uintptr
	}
	wc := windowClass{WndProc: proc.Addr(), Instance: instance, ClassName: class}
	wc.Size = uint32(unsafe.Sizeof(wc))
	atom, _, err := badgeUser32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		t.Fatal(err)
	}
	defer badgeUser32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(class)), instance)
	hwnd, _, err := badgeUser32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(class)), 0, 0, 0, 100, 100, 0, 0, instance, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer badgeUser32.NewProc("DestroyWindow").Call(hwnd)
	target := findBadgeTarget()
	if target.window != hwnd {
		t.Fatal("wrong process/class target", target)
	}
	if target.shell == 0 {
		t.Skip("interactive Windows Shell unavailable")
	}
	resources := badgeUser32.NewProc("GetGuiResources")
	process, _, _ := kernel.NewProc("GetCurrentProcess").Call()
	measure := func(flag uintptr) uintptr { result, _, _ := resources.Call(process, flag); return result }
	// Warm COM/Shell initialization before measuring per-update resources.
	if err := applyTaskbarBadge(target, taskbarStatus{count: 1}); err != nil {
		t.Fatal(err)
	}
	baselineGDI, baselineUSER := measure(0), measure(1)
	for i := 0; i < 100; i++ {
		status := []taskbarStatus{{count: 1}, {count: 9}, {count: 10}, {attention: true}, {count: 2, attention: true}, {}}[i%6]
		if err := applyTaskbarBadge(target, status); err != nil {
			t.Fatal(status, err)
		}
		if findBadgeTarget().window != hwnd {
			t.Fatal("callback changed window target")
		}
	}
	if err := applyTaskbarBadge(target, taskbarStatus{}); err != nil {
		t.Fatal(err)
	}
	if after := measure(0); after > baselineGDI+2 {
		t.Fatalf("GDI leak: %d -> %d", baselineGDI, after)
	}
	if after := measure(1); after > baselineUSER+2 {
		t.Fatalf("USER/icon leak: %d -> %d", baselineUSER, after)
	}
}
