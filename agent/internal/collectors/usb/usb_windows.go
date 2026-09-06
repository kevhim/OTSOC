//go:build windows
// +build windows

package usb

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procRegisterClassExW             = user32.NewProc("RegisterClassExW")
	procCreateWindowExW              = user32.NewProc("CreateWindowExW")
	procDefWindowProcW               = user32.NewProc("DefWindowProcW")
	procDestroyWindow                = user32.NewProc("DestroyWindow")
	procUnregisterClassW             = user32.NewProc("UnregisterClassW")
	procGetMessageW                  = user32.NewProc("GetMessageW")
	procTranslateMessage             = user32.NewProc("TranslateMessage")
	procDispatchMessageW             = user32.NewProc("DispatchMessageW")
	procPostMessageW                 = user32.NewProc("PostMessageW")
	procRegisterDeviceNotificationW  = user32.NewProc("RegisterDeviceNotificationW")
	procUnregisterDeviceNotification = user32.NewProc("UnregisterDeviceNotification")
)

const (
	WM_DEVICECHANGE             = 0x0219
	WM_CLOSE                    = 0x0010
	DBT_DEVICEARRIVAL           = 0x8000
	DBT_DEVICEREMOVECOMPLETE    = 0x8004
	DBT_DEVTYP_DEVICEINTERFACE  = 0x00000005
	DEVICE_NOTIFY_WINDOW_HANDLE = 0x00000000
)

type WNDCLASSEXW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type MSG struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      POINT
}

type POINT struct {
	x, y int32
}

type DEV_BROADCAST_DEVICEINTERFACE struct {
	dbcc_size       uint32
	dbcc_devicetype uint32
	dbcc_reserved   uint32
	dbcc_classguid  syscall.GUID
	dbcc_name       [1]uint16
}

type DEV_BROADCAST_HDR struct {
	dbch_size       uint32
	dbch_devicetype uint32
	dbch_reserved   uint32
}

// GUID_DEVINTERFACE_USB_DEVICE {A5DCBF10-6530-11D2-901F-00C04FB951ED}
var guidUSBDevice = syscall.GUID{
	Data1: 0xA5DCBF10,
	Data2: 0x6530,
	Data3: 0x11D2,
	Data4: [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED},
}

var (
	watcherMu  sync.Mutex
	watcherOut chan<- USBEvent
)

func defaultStartOSWatcher(ctx context.Context, out chan<- USBEvent) error {
	watcherMu.Lock()
	if watcherOut != nil {
		watcherMu.Unlock()
		return fmt.Errorf("only one usb watcher can run at a time")
	}
	watcherOut = out
	watcherMu.Unlock()

	defer func() {
		watcherMu.Lock()
		watcherOut = nil
		watcherMu.Unlock()
	}()

	errCh := make(chan error, 1)
	hwndCh := make(chan syscall.Handle, 1)

	go func() {
		className, err := syscall.UTF16PtrFromString("RedCyberFoxUSBWatcher")
		if err != nil {
			errCh <- err
			return
		}

		wndprocCB := syscall.NewCallback(wndProc)

		wndClass := WNDCLASSEXW{
			cbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
			lpfnWndProc:   wndprocCB,
			hInstance:     0,
			lpszClassName: className,
		}

		ret, _, errCode := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClass)))
		if ret == 0 {
			errCh <- fmt.Errorf("RegisterClassExW failed: %v", errCode)
			return
		}
		defer procUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), 0)

		hwnd, _, errCode := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(className)),
			0, // hidden
			0, 0, 0, 0,
			0, // HWND_MESSAGE
			0,
			0,
			0,
		)
		if hwnd == 0 {
			errCh <- fmt.Errorf("CreateWindowExW failed: %v", errCode)
			return
		}
		defer procDestroyWindow.Call(hwnd)

		// Register for device notifications
		var filter DEV_BROADCAST_DEVICEINTERFACE
		filter.dbcc_size = uint32(unsafe.Sizeof(filter))
		filter.dbcc_devicetype = DBT_DEVTYP_DEVICEINTERFACE
		filter.dbcc_classguid = guidUSBDevice

		hDevNotify, _, errCode := procRegisterDeviceNotificationW.Call(
			hwnd,
			uintptr(unsafe.Pointer(&filter)),
			DEVICE_NOTIFY_WINDOW_HANDLE,
		)
		if hDevNotify == 0 {
			errCh <- fmt.Errorf("RegisterDeviceNotificationW failed: %v", errCode)
			return
		}
		defer procUnregisterDeviceNotification.Call(hDevNotify)

		hwndCh <- syscall.Handle(hwnd)

		var msg MSG
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()

	var hwnd syscall.Handle
	select {
	case err := <-errCh:
		return err
	case hwnd = <-hwndCh:
	case <-ctx.Done():
		return nil
	}

	<-ctx.Done()

	// Safely post a message to wake up GetMessageW and terminate the loop
	procPostMessageW.Call(uintptr(hwnd), WM_CLOSE, 0, 0)

	return nil
}

func wndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_DEVICECHANGE:
		if wParam == DBT_DEVICEARRIVAL || wParam == DBT_DEVICEREMOVECOMPLETE {
			if lParam != 0 {
				hdr := (*DEV_BROADCAST_HDR)(unsafe.Pointer(lParam))
				if hdr.dbch_devicetype == DBT_DEVTYP_DEVICEINTERFACE {
					devInterface := (*DEV_BROADCAST_DEVICEINTERFACE)(unsafe.Pointer(lParam))

					nameSlice := (*[1024]uint16)(unsafe.Pointer(&devInterface.dbcc_name[0]))[:]

					var length int
					for i, v := range nameSlice {
						if v == 0 {
							length = i
							break
						}
					}

					pathStr := syscall.UTF16ToString(nameSlice[:length])

					action := "USB_INSERT"
					if wParam == DBT_DEVICEREMOVECOMPLETE {
						action = "USB_REMOVE"
					}

					vid, pid, serial := parseDevicePath(pathStr)

					watcherMu.Lock()
					if watcherOut != nil {
						watcherOut <- USBEvent{
							Action:       action,
							VendorID:     vid,
							ProductID:    pid,
							SerialNumber: serial,
							DevicePath:   pathStr,
							OccurredAt:   time.Now().UTC(),
						}
					}
					watcherMu.Unlock()
				}
			}
		}
	case WM_CLOSE:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

var vidPidRegex = regexp.MustCompile(`(?i)VID_([0-9A-F]{4})&PID_([0-9A-F]{4})`)

func parseDevicePath(path string) (vid, pid, serial string) {
	// Example path: \\?\USB#VID_1234&PID_5678#SERIALNUMBER#{guid}

	matches := vidPidRegex.FindStringSubmatch(path)
	if len(matches) == 3 {
		vid = strings.ToLower(matches[1])
		pid = strings.ToLower(matches[2])
	}

	parts := strings.Split(path, "#")
	if len(parts) >= 4 {
		// Usually parts[2] is the serial or an instance ID
		if parts[2] != "" && !strings.Contains(parts[2], "&") {
			// standard serials typically don't have &
			serial = parts[2]
		}
	}
	return
}
