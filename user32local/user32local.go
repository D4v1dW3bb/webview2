package user32local

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	LWA_ALPHA    = 0x00000002
	LWA_COLORKEY = 0x00000001
)

var (
	libuser32 *windows.LazyDLL

	setLayeredWindowAttributes *windows.LazyProc
	setWindowTextW             *windows.LazyProc
)

func init() {

	// Libary
	libuser32 = windows.NewLazySystemDLL("user32.dll")

	// Functions
	setLayeredWindowAttributes = libuser32.NewProc("SetLayeredWindowAttributes")
	setWindowTextW = libuser32.NewProc("SetWindowTextW")
}

func SetLayeredWindowAttributes(hwnd win.HWND, crKey win.COLORREF, bAlpha byte, dwFlags uint32) bool {

	ret, _, _ := syscall.Syscall6(setLayeredWindowAttributes.Addr(), 4,
		uintptr(hwnd),
		uintptr(crKey),
		uintptr(bAlpha),
		uintptr(dwFlags),
		0,
		0)

	return ret != 0
}

func SetWindowText(hwnd win.HWND, title *uint16) bool {
	ret, _, _ := syscall.Syscall(setWindowTextW.Addr(), 2,
		uintptr(hwnd),
		uintptr(unsafe.Pointer(title)),
		0)

	return ret != 0
}
