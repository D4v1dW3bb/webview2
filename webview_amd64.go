package webview2

import (
	"unsafe"

	"github.com/lxn/win"
)

func (e *chromiumedge) Resize() {
	if e.controller == nil {
		return
	}
	// var bounds _Rect
	var bounds win.RECT

	// user32GetClientRect.Call(e.hwnd, uintptr(unsafe.Pointer(&bounds)))
	win.GetClientRect(win.HWND(e.hwnd), &bounds)

	e.controller.vtbl.PutBounds.Call(
		uintptr(unsafe.Pointer(e.controller)),
		uintptr(unsafe.Pointer(&bounds)),
	)
}
