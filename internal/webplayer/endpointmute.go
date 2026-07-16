package webplayer

// 端点静音控制：远程静音电脑扬声器。
//
// 本机实测（Realtek，QueryHardwareSupport=0x3 硬件音量/静音）：WASAPI loopback
// 的抓取点位于端点音量/静音之前，静音扬声器不影响采集数据——手机端串流照常
// 有声，这正是"服务端静音"的目标语义。（软件音量的设备上静音会连带串流无声，
// 属于该方案的已知限制，届时需改用进程环回采集。）
//
// 个别驱动（本机 Realtek 即如此）IAudioEndpointVolume.SetMute 返回
// ERROR_INVALID_FUNCTION，故失败时回退 VK_VOLUME_MUTE 媒体键切换，
// 并用 GetMute 轮询校验实现"目标态"语义（幂等，不怕重复点击）。

import (
	"fmt"
	"runtime"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"

	"audiostream/internal/logx"
)

// withEndpointVolume 在独立锁定线程上初始化 COM 并激活默认渲染端点的
// IAudioEndpointVolume。与 audiosession.go 相同的每次调用独立初始化模式。
func withEndpointVolume(fn func(aev *wca.IAudioEndpointVolume) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return fmt.Errorf("COM 初始化失败: %w", err)
	}
	defer ole.CoUninitialize()

	var deviceEnum *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator, 0, ole.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &deviceEnum,
	); err != nil {
		return fmt.Errorf("创建设备枚举器失败: %w", err)
	}
	defer deviceEnum.Release()

	var device *wca.IMMDevice
	if err := deviceEnum.GetDefaultAudioEndpoint(wca.ERender, wca.EMultimedia, &device); err != nil {
		return fmt.Errorf("获取默认音频设备失败: %w", err)
	}
	defer device.Release()

	var aev *wca.IAudioEndpointVolume
	if err := device.Activate(wca.IID_IAudioEndpointVolume, ole.CLSCTX_ALL, nil, &aev); err != nil {
		return fmt.Errorf("激活 IAudioEndpointVolume 失败: %w", err)
	}
	defer aev.Release()

	return fn(aev)
}

// QueryEndpointMute 返回默认渲染端点是否静音；查询失败时 ok=false。
func QueryEndpointMute() (muted, ok bool) {
	err := withEndpointVolume(func(aev *wca.IAudioEndpointVolume) error {
		return aev.GetMute(&muted)
	})
	if err != nil {
		logx.Debugf("media", "[EndpointMute] GetMute 失败: %v", err)
		return false, false
	}
	return muted, true
}

// SetEndpointMute 把默认渲染端点静音状态设为 target（幂等）。
func SetEndpointMute(target bool) error {
	if m, ok := QueryEndpointMute(); ok && m == target {
		return nil
	}

	errSet := withEndpointVolume(func(aev *wca.IAudioEndpointVolume) error {
		return aev.SetMute(target, nil)
	})
	if errSet == nil {
		if m, ok := QueryEndpointMute(); ok && m == target {
			return nil
		}
		errSet = fmt.Errorf("SetMute 返回成功但状态未变化")
	}

	// 回退：VK_VOLUME_MUTE 媒体键切换。SendInput 异步生效，轮询确认。
	logx.Debugf("media", "[EndpointMute] SetMute 不可用(%v)，回退媒体键", errSet)
	sendMediaKey(VK_VOLUME_MUTE)
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		if m, ok := QueryEndpointMute(); ok && m == target {
			return nil
		}
	}
	return fmt.Errorf("静音设置未生效 (SetMute: %v)", errSet)
}
