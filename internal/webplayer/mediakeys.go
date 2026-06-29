package webplayer

import (
	"log"
	"syscall"
	"unsafe"

	"audiostream/internal/logx"
)

const (
	VK_MEDIA_NEXT_TRACK = 0xB0
	VK_MEDIA_PREV_TRACK = 0xB1
	VK_MEDIA_PLAY_PAUSE = 0xB3

	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYDOWN = 0x0000
	KEYEVENTF_KEYUP   = 0x0002
)

var (
	modUser32     = syscall.NewLazyDLL("user32.dll")
	procSendInput = modUser32.NewProc("SendInput")
)

// Windows INPUT 结构体 (x64):
//   DWORD type       = 4 bytes, offset 0
//   DWORD padding    = 4 bytes, offset 4  (union 8-byte alignment)
//   union {
//     MOUSEINPUT mi    = 32 bytes
//     KEYBDINPUT ki    = 24 bytes  ← 我们只用这个
//     HARDWAREINPUT hi = 8  bytes
//   }                = 32 bytes, offset 8
//   total: sizeof(INPUT) = 40 bytes

type keybdInput struct {
	wVk         uint16   // offset 0
	wScan       uint16   // offset 2
	dwFlags     uint32   // offset 4
	time        uint32   // offset 8
	_           [4]byte  // padding for dwExtraInfo 8-byte alignment
	dwExtraInfo uintptr  // offset 16
} // total: 24 bytes

type input struct {
	type_ uint32     // offset 0
	_     [4]byte    // offset 4 (union alignment padding)
	ki    keybdInput // offset 8, 24 bytes
	_     [8]byte    // offset 32, 8 bytes padding to match sizeof(INPUT)=40
}

func sendMediaKey(vk uint16) {
	logx.Debugf("media", "[MediaKeys] 发送媒体键 vkey=0x%02X", vk)
	in := input{
		type_: INPUT_KEYBOARD,
		ki: keybdInput{
			wVk:     vk,
			dwFlags: KEYEVENTF_KEYDOWN,
		},
	}

	ret, _, _ := procSendInput.Call(
		1,
		uintptr(unsafe.Pointer(&in)),
		unsafe.Sizeof(in),
	)
	if ret == 0 {
		log.Printf("[AudioStream] SendInput(KEY_DOWN) 失败，vkey=0x%02X", vk)
	}

	// KEY_UP
	in.ki.dwFlags = KEYEVENTF_KEYUP
	in.ki.time = 0

	ret, _, _ = procSendInput.Call(
		1,
		uintptr(unsafe.Pointer(&in)),
		unsafe.Sizeof(in),
	)
	if ret == 0 {
		log.Printf("[AudioStream] SendInput(KEY_UP) 失败，vkey=0x%02X", vk)
	}
}

func ExecuteMediaCommand(cmd *MediaCommand) {
	switch cmd.Action {
	case "play_pause":
		sendMediaKey(VK_MEDIA_PLAY_PAUSE)
		log.Printf("[WebPlayer] 播放/暂停")
	case "previous":
		sendMediaKey(VK_MEDIA_PREV_TRACK)
		log.Printf("[WebPlayer] 上一曲")
	case "next":
		sendMediaKey(VK_MEDIA_NEXT_TRACK)
		log.Printf("[WebPlayer] 下一曲")
	case "seek_to":
		if cmd.Position >= 0 {
			ExecuteSeek(cmd.Position)
		}
	case "set_volume":
		ExecuteSetVolume(cmd.Volume)
	}
}
