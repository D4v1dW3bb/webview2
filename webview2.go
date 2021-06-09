// +build windows

package webview2

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/d4v1dw3bb/webview2/user32local"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	ole32               = windows.NewLazySystemDLL("ole32")
	ole32CoInitializeEx = ole32.NewProc("CoInitializeEx")

	kernel32                   = windows.NewLazySystemDLL("kernel32")
	kernel32GetProcessHeap     = kernel32.NewProc("GetProcessHeap")
	kernel32HeapAlloc          = kernel32.NewProc("HeapAlloc")
	kernel32HeapFree           = kernel32.NewProc("HeapFree")
	kernel32GetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")

	defaultHeap uintptr
)

var (
	windowContext     = map[uintptr]interface{}{}
	windowContextSync sync.RWMutex
)

func getWindowContext(wnd uintptr) interface{} {
	windowContextSync.RLock()
	defer windowContextSync.RUnlock()
	return windowContext[wnd]
}

func setWindowContext(wnd uintptr, data interface{}) {
	windowContextSync.Lock()
	defer windowContextSync.Unlock()
	windowContext[wnd] = data
}

type _WndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cnClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type _Rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type _Point struct {
	x, y int32
}

type _Msg struct {
	hwnd     syscall.Handle
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       _Point
	lPrivate uint32
}

type _MinMaxInfo struct {
	ptReserved     _Point
	ptMaxSize      _Point
	ptMaxPosition  _Point
	ptMinTrackSize _Point
	ptMaxTrackSize _Point
}

func init() {
	runtime.LockOSThread()

	r, _, _ := ole32CoInitializeEx.Call(0, 2)
	if r < 0 {
		log.Printf("Warning: CoInitializeEx call failed: E=%08x", r)
	}

	defaultHeap, _, _ = kernel32GetProcessHeap.Call()
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	// Find NUL terminator.
	end := unsafe.Pointer(p)
	n := 0
	for *(*uint16)(end) != 0 {
		end = unsafe.Pointer(uintptr(end) + unsafe.Sizeof(*p))
		n++
	}
	s := (*[(1 << 30) - 1]uint16)(unsafe.Pointer(p))[:n:n]
	return string(utf16.Decode(s))
}

type chromiumedge struct {
	hwnd                uintptr
	controller          *iCoreWebView2Controller
	webview             *iCoreWebView2
	inited              uintptr
	envCompleted        *iCoreWebView2CreateCoreWebView2EnvironmentCompletedHandler
	controllerCompleted *iCoreWebView2CreateCoreWebView2ControllerCompletedHandler
	webMessageReceived  *iCoreWebView2WebMessageReceivedEventHandler
	permissionRequested *iCoreWebView2PermissionRequestedEventHandler
	msgcb               func(string)
}

type browser interface {
	Embed(hwnd uintptr) bool
	Resize()
	Navigate(url string)
	Init(script string)
	Eval(script string)
}

type webview struct {
	hwnd       uintptr
	mainthread uintptr
	browser    browser
	maxsz      _Point
	minsz      _Point
}

func newchromiumedge() *chromiumedge {
	e := &chromiumedge{}
	e.envCompleted = newICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler(e)
	e.controllerCompleted = newICoreWebView2CreateCoreWebView2ControllerCompletedHandler(e)
	e.webMessageReceived = newICoreWebView2WebMessageReceivedEventHandler(e)
	e.permissionRequested = newICoreWebView2PermissionRequestedEventHandler(e)
	return e
}

func (e *chromiumedge) Embed(hwnd uintptr) bool {
	e.hwnd = hwnd
	currentExePath := make([]uint16, windows.MAX_PATH)
	windows.GetModuleFileName(windows.Handle(0), &currentExePath[0], windows.MAX_PATH)
	currentExeName := filepath.Base(windows.UTF16ToString(currentExePath))
	dataPath := filepath.Join(os.Getenv("AppData"), currentExeName)
	res, err := createCoreWebView2EnvironmentWithOptions(nil, windows.StringToUTF16Ptr(dataPath), 0, e.envCompleted)
	if err != nil {
		log.Printf("Error calling Webview2Loader: %v", err)
		return false
	} else if res != 0 {
		log.Printf("Result: %08x", res)
		return false
	}
	var msg win.MSG
	for {
		if atomic.LoadUintptr(&e.inited) != 0 {
			break
		}

		r := win.GetMessage(&msg, 0, 0, 0)
		if r == 0 {
			break
		}
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
	e.Init("window.external={invoke:s=>window.chrome.webview.postMessage(s)}")
	return true
}

func (e *chromiumedge) Navigate(url string) {
	e.webview.vtbl.Navigate.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(url))),
	)
}

func (e *chromiumedge) Init(script string) {
	e.webview.vtbl.AddScriptToExecuteOnDocumentCreated.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(script))),
		0,
	)
}

func (e *chromiumedge) Eval(script string) {
	e.webview.vtbl.ExecuteScript.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(script))),
		0,
	)
}

func (e *chromiumedge) QueryInterface(refiid, object uintptr) uintptr {
	return 0
}

func (e *chromiumedge) AddRef() uintptr {
	return 1
}

func (e *chromiumedge) Release() uintptr {
	return 1
}

func (e *chromiumedge) EnvironmentCompleted(res uintptr, env *iCoreWebView2Environment) uintptr {
	if int64(res) < 0 {
		log.Fatalf("Creating environment failed with %08x", res)
	}
	env.vtbl.CreateCoreWebView2Controller.Call(
		uintptr(unsafe.Pointer(env)),
		e.hwnd,
		uintptr(unsafe.Pointer(e.controllerCompleted)),
	)
	return 0
}

func (e *chromiumedge) ControllerCompleted(res uintptr, controller *iCoreWebView2Controller) uintptr {
	if int64(res) < 0 {
		log.Fatalf("Creating controller failed with %08x", res)
	}
	controller.vtbl.AddRef.Call(uintptr(unsafe.Pointer(controller)))
	e.controller = controller

	var token _EventRegistrationToken
	controller.vtbl.GetCoreWebView2.Call(
		uintptr(unsafe.Pointer(controller)),
		uintptr(unsafe.Pointer(&e.webview)),
	)
	e.webview.vtbl.AddRef.Call(
		uintptr(unsafe.Pointer(e.webview)),
	)
	e.webview.vtbl.AddWebMessageReceived.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(e.webMessageReceived)),
		uintptr(unsafe.Pointer(&token)),
	)
	e.webview.vtbl.AddPermissionRequested.Call(
		uintptr(unsafe.Pointer(e.webview)),
		uintptr(unsafe.Pointer(e.permissionRequested)),
		uintptr(unsafe.Pointer(&token)),
	)

	atomic.StoreUintptr(&e.inited, 1)

	return 0
}

func (e *chromiumedge) MessageReceived(sender *iCoreWebView2, args *iCoreWebView2WebMessageReceivedEventArgs) uintptr {
	var message *uint16
	args.vtbl.TryGetWebMessageAsString.Call(
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(&message)),
	)
	e.msgcb(utf16PtrToString(message))
	sender.vtbl.PostWebMessageAsString.Call(
		uintptr(unsafe.Pointer(sender)),
		uintptr(unsafe.Pointer(message)),
	)
	windows.CoTaskMemFree(unsafe.Pointer(message))
	return 0
}

func (e *chromiumedge) PermissionRequested(sender *iCoreWebView2, args *iCoreWebView2PermissionRequestedEventArgs) uintptr {
	var kind _CoreWebView2PermissionKind
	args.vtbl.GetPermissionKind.Call(
		uintptr(unsafe.Pointer(args)),
		uintptr(kind),
	)
	if kind == _CoreWebView2PermissionKindClipboardRead {
		args.vtbl.PutState.Call(
			uintptr(unsafe.Pointer(args)),
			uintptr(_CoreWebView2PermissionStateAllow),
		)
	}
	return 0
}

// New creates a new webview in a new window.
func New(debug bool) WebView { return NewWindow(debug, nil) }

// NewWindow creates a new webview using an existing window.
func NewWindow(debug bool, window unsafe.Pointer) WebView {
	w := &webview{}
	w.browser = newchromiumedge()
	w.mainthread, _, _ = kernel32GetCurrentThreadID.Call()
	if !w.Create(debug, window) {
		return nil
	}
	return w
}

func wndproc(hwnd, msg, wp, lp uintptr) uintptr {
	if w, ok := getWindowContext(hwnd).(*webview); ok {
		switch msg {
		case win.WM_SIZE:
			w.browser.Resize()
		case win.WM_CLOSE:
			win.DestroyWindow(win.HWND(hwnd))
		case win.WM_DESTROY:
			w.Terminate()
		case win.WM_GETMINMAXINFO:
			lpmmi := (*_MinMaxInfo)(unsafe.Pointer(lp))
			if w.maxsz.x > 0 && w.maxsz.y > 0 {
				lpmmi.ptMaxSize = w.maxsz
				lpmmi.ptMaxTrackSize = w.maxsz
			}
			if w.minsz.x > 0 && w.minsz.y > 0 {
				lpmmi.ptMinTrackSize = w.minsz
			}
		default:
			r := win.DefWindowProc(win.HWND(hwnd), uint32(msg), wp, lp)
			return r
		}
		return 0
	}
	r := win.DefWindowProc(win.HWND(hwnd), uint32(msg), wp, lp)
	return r
}

func (w *webview) Create(debug bool, window unsafe.Pointer) bool {
	var hinstance windows.Handle
	windows.GetModuleHandleEx(0, nil, &hinstance)

	// icow, _, _ := user32GetSystemMetrics.Call(_SystemMetricsCxIcon)
	icow := win.GetSystemMetrics(win.SM_CXICON)
	// icoh, _, _ := user32GetSystemMetrics.Call(_SystemMetricsCyIcon)
	icoh := win.GetSystemMetrics(win.SM_CYICON)

	// icon, _, _ := user32LoadImageW.Call(uintptr(hinstance), 32512, icow, icoh, 0)
	icon := win.LoadImage(win.HINSTANCE(hinstance), windows.StringToUTF16Ptr("webview"), win.IDI_APPLICATION, icow, icoh, 0)
	// wc := _WndClassExW{
	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		HInstance:     win.HINSTANCE(hinstance),
		LpszClassName: windows.StringToUTF16Ptr("webview"),
		HIcon:         win.HICON(icon),
		HIconSm:       win.HICON(icon),
		LpfnWndProc:   windows.NewCallback(wndproc),
	}

	// user32RegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	win.RegisterClassEx(&wc)

	// w.hwnd, _, _ = user32CreateWindowExW.Call(
	w.hwnd = uintptr(win.CreateWindowEx(
		0,
		windows.StringToUTF16Ptr("webview"),
		windows.StringToUTF16Ptr(""),
		win.WS_OVERLAPPEDWINDOW,
		win.CW_USEDEFAULT,
		win.CW_USEDEFAULT,
		640,
		480,
		0,
		0,
		win.HINSTANCE(hinstance),
		nil,
	))
	setWindowContext(w.hwnd, w)

	// user32ShowWindow.Call(w.hwnd, _SWShow)
	win.ShowWindow(win.HWND(w.hwnd), win.SW_SHOW)

	//user32UpdateWindow.Call(w.hwnd)
	win.UpdateWindow(win.HWND(w.hwnd))

	//user32SetFocus.Call(w.hwnd)
	win.SetFocus(win.HWND(w.hwnd))

	if !w.browser.Embed(w.hwnd) {
		return false
	}
	w.browser.Resize()
	return true
}

func (w *webview) Destroy() {
}

func (w *webview) Run() {
	var msg win.MSG
	for {
		// user32GetMessageW.Call(
		win.GetMessage(
			&msg,
			0,
			0,
			0,
		)

		if msg.Message == win.WM_APP {

		} else if msg.Message == win.WM_QUIT {
			return
		}

		//user32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		win.TranslateMessage(&msg)

		//user32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		win.DispatchMessage(&msg)
	}
}

func (w *webview) Terminate() {
	//user32PostQuitMessage.Call(0)
	win.PostQuitMessage(0)
}

func (w *webview) Window() unsafe.Pointer {
	return unsafe.Pointer(w.hwnd)
}

func (w *webview) Navigate(url string) {
	w.browser.Navigate(url)
}

func (w *webview) SetTitle(title string) {
	// user32SetWindowTextW.Call(w.hwnd, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))))
	user32local.SetWindowText(win.HWND(w.hwnd), windows.StringToUTF16Ptr(title))
}

func (w *webview) SetSize(width int, height int, hints Hint) {
	index := win.GWL_STYLE

	//style, _, _ := user32GetWindowLongPtrW.Call(w.hwnd, uintptr(index))
	style := win.GetWindowLongPtr(win.HWND(w.hwnd), int32(index))

	if hints == HintFixed {
		style &^= (win.WS_THICKFRAME | win.WS_MAXIMIZEBOX)
	} else {
		style |= (win.WS_THICKFRAME | win.WS_MAXIMIZEBOX)
	}

	//user32SetWindowLongPtrW.Call(w.hwnd, uintptr(index), style)
	win.SetWindowLongPtr(win.HWND(w.hwnd), index, style)

	if hints == HintMax {
		w.maxsz.x = int32(width)
		w.maxsz.y = int32(height)
	} else if hints == HintMin {
		w.minsz.x = int32(width)
		w.minsz.y = int32(height)
	} else {
		r := win.RECT{}
		r.Left = 0
		r.Top = 0
		r.Right = int32(width)
		r.Bottom = int32(height)
		// user32AdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), _WSOverlappedWindow, 0)
		win.AdjustWindowRect(&r, win.WS_OVERLAPPEDWINDOW, false)

		// user32SetWindowPos.Call(
		//	w.hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
		//	_SWPNoZOrder|_SWPNoActivate|_SWPNoMove|_SWPFrameChanged)
		win.SetWindowPos(win.HWND(w.hwnd), 0, r.Left, r.Top, r.Right-r.Left, r.Bottom-r.Top, win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_NOMOVE|win.SWP_FRAMECHANGED)

		w.browser.Resize()
	}
}

func (w *webview) Init(js string) {
	w.browser.Init(js)
}

func (w *webview) Eval(js string) {
	w.browser.Eval(js)
}

func (w *webview) SetTransparentBackground(hwnd win.HWND) {
	win.SetWindowLong(hwnd, win.GWL_EXSTYLE, win.GetWindowLong(hwnd, win.GWL_EXSTYLE)|win.WS_EX_LAYERED|win.WS_EX_NOACTIVATE)
	win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOSIZE|win.SWP_NOMOVE|win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW)
	user32local.SetLayeredWindowAttributes(hwnd, win.RGB(255, 255, 255), 0, user32local.LWA_ALPHA)
}

func (w *webview) Dispatch(f func()) {
	// TODO
}

func (w *webview) Bind(name string, f interface{}) error {
	// TODO
	return nil
}
