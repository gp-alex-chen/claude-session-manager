//go:build windows

package notify

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

var badgeUser32 = syscall.NewLazyDLL("user32.dll")
var badgeGDI32 = syscall.NewLazyDLL("gdi32.dll")
var enumBadgeWindows = badgeUser32.NewProc("EnumWindows")
var badgeWindowPID = badgeUser32.NewProc("GetWindowThreadProcessId")
var badgeWindowClass = badgeUser32.NewProc("GetClassNameW")
var badgeShellWindow = badgeUser32.NewProc("GetShellWindow")
var badgeCreateDIB = badgeGDI32.NewProc("CreateDIBSection")
var badgeCreateBitmap = badgeGDI32.NewProc("CreateBitmap")
var badgeDeleteObject = badgeGDI32.NewProc("DeleteObject")
var badgeCreateIcon = badgeUser32.NewProc("CreateIconIndirect")
var badgeDestroyIcon = badgeUser32.NewProc("DestroyIcon")
var badgeEnumMu sync.Mutex
var badgeEnumWindow uintptr

// NewCallback allocates a permanent trampoline; create exactly one.
var badgeEnumCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
	var pid uint32
	badgeWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != uint32(os.Getpid()) {
		return 1
	}
	var name [128]uint16
	badgeWindowClass.Call(hwnd, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	if syscall.UTF16ToString(name[:]) == WindowClassName {
		badgeEnumWindow = hwnd
		return 0
	}
	return 1
})

func findBadgeTarget() badgeTarget {
	badgeEnumMu.Lock()
	defer badgeEnumMu.Unlock()
	target := badgeTarget{}
	target.shell, _, _ = badgeShellWindow.Call()
	badgeEnumWindow = 0
	enumBadgeWindows.Call(badgeEnumCallback, 0)
	target.window = badgeEnumWindow
	return target
}

func applyTaskbarBadge(target badgeTarget, count int) error {
	// COM creation, use, Release and uninitialization stay on the same thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	if err == nil {
		defer ole.CoUninitialize()
	} else if code, ok := err.(*ole.OleError); ok && code.Code() == 1 {
		defer ole.CoUninitialize() // S_FALSE is still a successful initialization.
	} else if !ok || uint32(code.Code()) != 0x80010106 {
		return err
	} // RPC_E_CHANGED_MODE: use the existing apartment.
	object, err := ole.CreateInstance(ole.NewGUID("{56FDF344-FD6D-11D0-958A-006097C9A090}"), ole.NewGUID("{EA1AFB91-9E28-4B86-90E9-9E9F8A5EEFAF}"))
	if err != nil {
		return err
	}
	defer object.Release()
	table := (*[21]uintptr)(unsafe.Pointer(object.RawVTable))
	self := uintptr(unsafe.Pointer(object))
	hr, _, _ := syscall.SyscallN(table[3], self) // ITaskbarList.HrInit
	if int32(hr) < 0 {
		return fmt.Errorf("初始化任务栏接口失败: 0x%x", hr)
	}
	var icon uintptr
	if count > 0 {
		icon, err = createBadgeIcon(count)
		if err != nil {
			return err
		}
		defer badgeDestroyIcon.Call(icon)
	}
	description, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%d 个会话已完成，尚未查看", count))
	hr, _, _ = syscall.SyscallN(table[18], self, target.window, icon, uintptr(unsafe.Pointer(description)))
	runtime.KeepAlive(object)
	runtime.KeepAlive(description)
	if int32(hr) < 0 {
		return fmt.Errorf("更新任务栏角标失败: 0x%x", hr)
	}
	return nil
}

// A small raster glyph keeps the overlay independent of installed fonts.
var badgeGlyphs = map[rune][7]string{
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "10000", "11110", "00001", "00001", "11110"},
	'6': {"01110", "10000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00001", "01110"},
	'+': {"00000", "00100", "00100", "11111", "00100", "00100", "00000"},
}

func badgeImage(count int) *image.RGBA {
	const size = 32
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			coverage := math.Max(0, math.Min(1, 16-math.Hypot(float64(x)-15.5, float64(y)-15.5)))
			a := uint8(coverage * 255)
			out.SetRGBA(x, y, color.RGBA{uint8(uint16(a) * 42 / 255), uint8(uint16(a) * 42 / 255), uint8(uint16(a) * 46 / 255), a})
		}
	}
	label := strconv.Itoa(count)
	if count > 9 {
		label = "9+"
	}
	scale := 3
	if len(label) > 1 {
		scale = 2
	}
	left, top := (size-(6*len(label)-1)*scale)/2, (size-7*scale)/2
	for i, letter := range label {
		for y, row := range badgeGlyphs[letter] {
			for x, pixel := range row {
				if pixel != '1' {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						out.SetRGBA(left+(i*6+x)*scale+dx, top+y*scale+dy, color.RGBA{255, 255, 255, 255})
					}
				}
			}
		}
	}
	return out
}

func createBadgeIcon(count int) (uintptr, error) {
	pixels := badgeImage(count)
	type bitmapHeader struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}
	header := bitmapHeader{Size: 40, Width: 32, Height: -32, Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	bitmap, _, err := badgeCreateDIB.Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 {
		return 0, fmt.Errorf("创建角标位图失败: %w", err)
	}
	defer badgeDeleteObject.Call(bitmap)
	data := unsafe.Slice((*byte)(bits), len(pixels.Pix))
	for i := 0; i < len(data); i += 4 {
		data[i], data[i+1], data[i+2], data[i+3] = pixels.Pix[i+2], pixels.Pix[i+1], pixels.Pix[i], pixels.Pix[i+3]
	}
	mask, _, err := badgeCreateBitmap.Call(32, 32, 1, 1, 0)
	if mask == 0 {
		return 0, fmt.Errorf("创建角标遮罩失败: %w", err)
	}
	defer badgeDeleteObject.Call(mask)
	type iconInfo struct {
		IsIcon, XHotspot, YHotspot uint32
		Mask, Color                uintptr
	}
	info := iconInfo{IsIcon: 1, Mask: mask, Color: bitmap}
	icon, _, err := badgeCreateIcon.Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		return 0, fmt.Errorf("创建角标失败: %w", err)
	}
	return icon, nil
}
