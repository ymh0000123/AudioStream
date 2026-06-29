package webplayer

import (
	_ "embed"
	"fmt"
	"log"
	"os/exec"
	"strconv"

	"audiostream/internal/logx"
)

//go:embed mediaseek.ps1
var seekScript string

// psPath holds the path to PowerShell, used by mediaseek/mediastate functions.
var psPath = func() string {
	p, err := exec.LookPath("powershell.exe")
	if err == nil {
		return p
	}
	p2, err2 := exec.LookPath("pwsh.exe")
	if err2 == nil {
		return p2
	}
	return ""
}()

func ExecuteSeek(positionMs int64) {
	if psPath == "" {
		log.Printf("[MediaSeek] PowerShell 不可用，无法 seek")
		return
	}

	cmd := exec.Command(psPath, "-NoProfile", "-NonInteractive",
		"-Command", seekScript,
		"-PositionMs", strconv.FormatInt(positionMs, 10))
	logx.Debugf("media", "[MediaSeek] 执行 seek: positionMs=%d", positionMs)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[MediaSeek] 执行 seek 失败 (pos=%dms): %v, out: %s", positionMs, err, string(out))
		return
	}
	log.Printf("[MediaSeek] seek 到 %dms", positionMs)
}

func ExecuteSetVolume(volume int) {
	if psPath == "" {
		log.Printf("[MediaSeek] PowerShell 不可用，无法设置音量")
		return
	}
	vol := volume
	if vol < 0 {
		vol = 0
	} else if vol > 100 {
		vol = 100
	}
	logx.Debugf("media", "[MediaSeek] 设置音量: 请求=%d, 钳制后=%d", volume, vol)

	psCmd := fmt.Sprintf(`
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Devices.Midi.Internal.MidiSrv,Windows.Devices.Midi,ContentType=WindowsRuntime]
$as = [Windows.Media.Devices.AudioDeviceController,Windows.Media.Devices,ContentType=WindowsRuntime]
try {
  $ep = [Windows.Media.Devices.AudioDeviceController]::GetDefaultAudioRenderEndpoint()
  $ep2 = $ep.GetAwaiter().GetResult()
  $vc = $ep2.GetVolumeControl()
  $vc2 = $vc.GetAwaiter().GetResult()
  $vc2.SetVolume(%d)
  Write-Output "ok"
} catch {
  Write-Output $_.Exception.Message
  exit 1
}
`, vol)

	cmd := exec.Command(psPath, "-NoProfile", "-NonInteractive", "-Command", psCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[MediaSeek] 设置音量失败: %v, out: %s", err, string(out))
	} else {
		log.Printf("[MediaSeek] 音量已设置为 %d: %s", vol, string(out))
	}
}
