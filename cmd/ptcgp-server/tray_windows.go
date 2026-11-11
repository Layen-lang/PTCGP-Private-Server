//go:build windows

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"runtime"
	"sync"
	"unsafe"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/sys/windows"
)

const (
	trayCallbackMessage = 0x0401
	trayIconID          = 1

	wmClose       = 0x0010
	wmDestroy     = 0x0002
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B

	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	imageApplicationIcon = 32512
	biBitfields          = 3
	dibRGBColors         = 0
	lcsSRGB              = 0x73524742

	menuOpen = 1001 + iota
	menuLocal
	menuOnline
	menuStop
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	gdi32                   = windows.NewLazySystemDLL("gdi32.dll")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap        = gdi32.NewProc("CreateBitmap")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
)

type trayWindowClass struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type trayPoint struct {
	X int32
	Y int32
}

type trayMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   trayPoint
	Private uint32
}

type trayIconData struct {
	Size            uint32
	Window          uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            windows.GUID
	BalloonIcon     uintptr
}

type trayReady struct {
	window uintptr
	err    error
}

type bitmapV5Header struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
	RedMask       uint32
	GreenMask     uint32
	BlueMask      uint32
	AlphaMask     uint32
	CSType        uint32
	Endpoints     [9]int32
	GammaRed      uint32
	GammaGreen    uint32
	GammaBlue     uint32
	Intent        uint32
	ProfileData   uint32
	ProfileSize   uint32
	Reserved      uint32
}

type iconInfo struct {
	Icon     int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

func startSystemTray(actions trayActions) (func(), error) {
	ready := make(chan trayReady, 1)
	done := make(chan struct{})
	go runSystemTray(actions, ready, done)
	result := <-ready
	if result.err != nil {
		return nil, result.err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			procPostMessageW.Call(result.window, wmClose, 0, 0)
			<-done
		})
	}, nil
}

func runSystemTray(actions trayActions, ready chan<- trayReady, done chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(done)

	instance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(fmt.Sprintf("PTCGPPrivateServerTray-%d", windows.GetCurrentProcessId()))
	windowName, _ := windows.UTF16PtrFromString("PTCGP Private Server")
	icon, releaseIcon, err := trayIconFromSVG(actions.IconSVG)
	if err != nil {
		ready <- trayReady{err: err}
		return
	}
	defer releaseIcon()

	var window uintptr
	windowProc := windows.NewCallback(func(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case trayCallbackMessage:
			switch uint32(lParam) {
			case wmLButtonUp:
				go actions.Open()
			case wmRButtonUp, wmContextMenu:
				showTrayMenu(hwnd, actions)
			}
			return 0
		case wmClose:
			procDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	})
	class := trayWindowClass{
		Size:       uint32(unsafe.Sizeof(trayWindowClass{})),
		WindowProc: windowProc,
		Instance:   instance,
		Icon:       icon,
		ClassName:  className,
		SmallIcon:  icon,
	}
	if atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		ready <- trayReady{err: fmt.Errorf("could not register the System Tray window")}
		return
	}
	window, _, _ = procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		ready <- trayReady{err: fmt.Errorf("could not create the System Tray window")}
		return
	}

	data := trayIconData{
		Size:            uint32(unsafe.Sizeof(trayIconData{})),
		Window:          window,
		ID:              trayIconID,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: trayCallbackMessage,
		Icon:            icon,
	}
	copy(data.Tip[:], windows.StringToUTF16("PTCGP Private Server"))
	if added, _, callErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data))); added == 0 {
		procDestroyWindow.Call(window)
		ready <- trayReady{err: fmt.Errorf("could not add the System Tray icon: %v", callErr)}
		return
	}
	defer procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	ready <- trayReady{window: window}

	var message trayMessage
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
	runtime.KeepAlive(windowProc)
}

func trayIconFromSVG(data []byte) (uintptr, func(), error) {
	if len(data) == 0 {
		icon, _, _ := procLoadIconW.Call(0, imageApplicationIcon)
		if icon == 0 {
			return 0, nil, fmt.Errorf("could not load the Windows icon")
		}
		return icon, func() {}, nil
	}
	vectorIcon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.WarnErrorMode)
	if err != nil {
		return 0, nil, fmt.Errorf("read System Tray logo: %w", err)
	}
	const size = 32
	source := image.NewNRGBA(image.Rect(0, 0, size, size))
	vectorIcon.SetTarget(0, 0, size, size)
	scanner := rasterx.NewScannerGV(size, size, source, source.Bounds())
	vectorIcon.Draw(rasterx.NewDasher(size, size, scanner), 1)
	header := bitmapV5Header{
		Size:        uint32(unsafe.Sizeof(bitmapV5Header{})),
		Width:       size,
		Height:      -size,
		Planes:      1,
		BitCount:    32,
		Compression: biBitfields,
		SizeImage:   size * size * 4,
		RedMask:     0x00ff0000,
		GreenMask:   0x0000ff00,
		BlueMask:    0x000000ff,
		AlphaMask:   0xff000000,
		CSType:      lcsSRGB,
	}
	var bits unsafe.Pointer
	colorBitmap, _, callErr := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&header)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if colorBitmap == 0 || bits == nil {
		return 0, nil, fmt.Errorf("could not create the System Tray bitmap: %v", callErr)
	}
	defer procDeleteObject.Call(colorBitmap)
	pixels := unsafe.Slice((*uint32)(bits), size*size)
	bounds := source.Bounds()
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/size
			sourceY := bounds.Min.Y + y*bounds.Dy()/size
			pixel := color.NRGBAModel.Convert(source.At(sourceX, sourceY)).(color.NRGBA)
			pixels[y*size+x] = uint32(pixel.A)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
		}
	}
	maskBits := make([]byte, size*size/8)
	maskBitmap, _, callErr := procCreateBitmap.Call(size, size, 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	if maskBitmap == 0 {
		return 0, nil, fmt.Errorf("could not create the System Tray mask: %v", callErr)
	}
	defer procDeleteObject.Call(maskBitmap)
	info := iconInfo{Icon: 1, Mask: maskBitmap, Color: colorBitmap}
	icon, _, callErr := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		return 0, nil, fmt.Errorf("could not create the System Tray logo: %v", callErr)
	}
	return icon, func() { procDestroyIcon.Call(icon) }, nil
}

func showTrayMenu(window uintptr, actions trayActions) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendTrayItem(menu, mfString, menuOpen, "Open control panel")
	appendTrayItem(menu, mfSeparator, 0, "")
	appendTrayItem(menu, mfString, menuLocal, "Local")
	appendTrayItem(menu, mfString, menuOnline, "Official")
	appendTrayItem(menu, mfSeparator, 0, "")
	appendTrayItem(menu, mfString, menuStop, "Stop all")

	var point trayPoint
	if ok, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point))); ok == 0 {
		return
	}
	procSetForegroundWindow.Call(window)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmNonotify|tpmReturnCmd, uintptr(point.X), uintptr(point.Y), 0, window, 0)
	switch command {
	case menuOpen:
		go actions.Open()
	case menuLocal:
		go actions.Local()
	case menuOnline:
		go actions.Online()
	case menuStop:
		go actions.Stop()
	}
}

func appendTrayItem(menu uintptr, flags uint32, id uintptr, label string) {
	if flags == mfSeparator {
		procAppendMenuW.Call(menu, uintptr(flags), id, 0)
		return
	}
	text, _ := windows.UTF16PtrFromString(label)
	procAppendMenuW.Call(menu, uintptr(flags), id, uintptr(unsafe.Pointer(text)))
	runtime.KeepAlive(text)
}
