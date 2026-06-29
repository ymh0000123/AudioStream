//go:build windows

package webplayer

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

type sessionCandidate struct {
	displayName string
	pid         uint32
}

// QueryActiveAudioSessions uses COM Core Audio API to enumerate active audio sessions.
func QueryActiveAudioSessions() (displayName string, pid uint32, ok bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return
	}
	defer ole.CoUninitialize()

	var deviceEnum *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator, 0, ole.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &deviceEnum,
	); err != nil {
		return
	}
	defer deviceEnum.Release()

	var device *wca.IMMDevice
	if err := deviceEnum.GetDefaultAudioEndpoint(wca.ERender, wca.EMultimedia, &device); err != nil {
		return
	}
	defer device.Release()

	var sessionMgr *wca.IAudioSessionManager2
	if err := device.Activate(
		wca.IID_IAudioSessionManager2, ole.CLSCTX_ALL, nil, &sessionMgr,
	); err != nil {
		return
	}
	defer sessionMgr.Release()

	var enumerator *wca.IAudioSessionEnumerator
	if err := sessionMgr.GetSessionEnumerator(&enumerator); err != nil {
		return
	}
	defer enumerator.Release()

	var count int
	if err := enumerator.GetCount(&count); err != nil {
		return
	}

	var candidates []sessionCandidate
	myPID := uint32(os.Getpid())
	for i := 0; i < count; i++ {
		var ctrl *wca.IAudioSessionControl
		if err := enumerator.GetSession(i, &ctrl); err != nil || ctrl == nil {
			continue
		}

		var state uint32
		if err := ctrl.GetState(&state); err != nil {
			ctrl.Release()
			continue
		}

		if state != wca.AudioSessionStateActive {
			ctrl.Release()
			continue
		}

		var name string
		ctrl.GetDisplayName(&name)

		var procID uint32
		disp, err := ctrl.IUnknown.QueryInterface(wca.IID_IAudioSessionControl2)
		if err == nil && disp != nil {
			ctrl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(disp))
			ctrl2.GetProcessId(&procID)
			ctrl2.Release()
		}
		ctrl.Release()

		if procID == 0 && name == "" {
			continue
		}
		if procID == myPID {
			continue
		}
		candidates = append(candidates, sessionCandidate{name, procID})
	}

	for _, c := range candidates {
		if c.displayName != "" {
			return c.displayName, c.pid, true
		}
	}

	for _, c := range candidates {
		return getProcessName(c.pid), c.pid, true
	}
	return "", 0, false
}

// getProcessName retrieves the executable name for a given PID.
func getProcessName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Sprintf("PID %d", pid)
	}
	defer syscall.CloseHandle(h)

	buf := make([]uint16, 260)
	size := uint32(len(buf))
	r1, _, _ := procQueryFullProcessImageNameW.Call(
		uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 {
		return fmt.Sprintf("PID %d", pid)
	}
	path := syscall.UTF16ToString(buf[:size])
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '\\' || path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

var (
	modkernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procQueryFullProcessImageNameW = modkernel32.NewProc("QueryFullProcessImageNameW")
)
