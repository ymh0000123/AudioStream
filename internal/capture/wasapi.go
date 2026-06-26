//go:build windows

package capture

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// wasapiLoopback 使用 WASAPI Loopback 捕获系统音频输出
type wasapiLoopback struct {
	format       Format
	deviceEnum   *wca.IMMDeviceEnumerator
	audioDevice  *wca.IMMDevice
	audioClient  *wca.IAudioClient
	captureCli   *wca.IAudioCaptureClient
	mixFormat    *wca.WAVEFORMATEX
	bufferFrames uint32
	blockAlign   uint32
	started      bool
	closed       bool
	mu           sync.Mutex
}

func newLoopback() (Capture, error) {
	// ★ 锁定 OS 线程：COM 要求所有操作在同一个线程上 ★
	// Go 的 goroutine 调度可能在线程间迁移，导致 COM 调用失败
	runtime.LockOSThread()

	// 初始化 COM
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("COM 初始化失败: %w", err)
	}

	wl := &wasapiLoopback{}

	// 1. 创建设备枚举器
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator,
		0,
		ole.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator,
		&wl.deviceEnum,
	); err != nil {
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("创建设备枚举器失败: %w", err)
	}

	// 2. 获取默认音频渲染设备（扬声器）
	if err := wl.deviceEnum.GetDefaultAudioEndpoint(
		wca.ERender,
		wca.EMultimedia,
		&wl.audioDevice,
	); err != nil {
		wl.deviceEnum.Release()
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("获取默认音频设备失败: %w", err)
	}

	// 3. 激活 IAudioClient
	if err := wl.audioDevice.Activate(
		wca.IID_IAudioClient,
		ole.CLSCTX_ALL,
		nil,
		&wl.audioClient,
	); err != nil {
		wl.audioDevice.Release()
		wl.deviceEnum.Release()
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("激活音频客户端失败: %w", err)
	}

	// 4. 获取混音格式
	if err := wl.audioClient.GetMixFormat(&wl.mixFormat); err != nil {
		wl.audioClient.Release()
		wl.audioDevice.Release()
		wl.deviceEnum.Release()
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("获取混音格式失败: %w", err)
	}

	// 解析音频格式
	wl.format = Format{
		SampleRate:    int(wl.mixFormat.NSamplesPerSec),
		Channels:      int(wl.mixFormat.NChannels),
		BitsPerSample: int(wl.mixFormat.WBitsPerSample),
	}

	wl.blockAlign = uint32(wl.mixFormat.NBlockAlign)

	// 5. 初始化音频客户端（Loopback 模式）
	// 使用 REFERENCE_TIME 格式：100 纳秒为单位
	// 缓冲持续时间设为 0，使用默认值
	if err := wl.audioClient.Initialize(
		wca.AUDCLNT_SHAREMODE_SHARED,
		wca.AUDCLNT_STREAMFLAGS_LOOPBACK,
		0, // 缓冲持续时间（默认）
		0, // 周期
		wl.mixFormat,
		nil, // 音频会话 GUID
	); err != nil {
		// 尝试带 AUTOCONVERTPCM 标志重试
		err2 := wl.audioClient.Initialize(
			wca.AUDCLNT_SHAREMODE_SHARED,
			wca.AUDCLNT_STREAMFLAGS_LOOPBACK|wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM,
			0,
			0,
			wl.mixFormat,
			nil,
		)
		if err2 != nil {
			wl.audioClient.Release()
			wl.audioDevice.Release()
			wl.deviceEnum.Release()
			ole.CoUninitialize()
			runtime.UnlockOSThread()
			return nil, fmt.Errorf("初始化音频客户端失败: %w (首次: %v)", err2, err)
		}
	}

	// 6. 获取缓冲区大小
	if err := wl.audioClient.GetBufferSize(&wl.bufferFrames); err != nil {
		wl.audioClient.Release()
		wl.audioDevice.Release()
		wl.deviceEnum.Release()
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("获取缓冲区大小失败: %w", err)
	}

	return wl, nil
}

func (wl *wasapiLoopback) Format() Format {
	return wl.format
}

func (wl *wasapiLoopback) Start() error {
	wl.mu.Lock()
	defer wl.mu.Unlock()

	if wl.closed {
		return fmt.Errorf("捕获器已关闭")
	}
	if wl.started {
		return nil
	}

	// 获取捕获客户端
	if err := wl.audioClient.GetService(
		wca.IID_IAudioCaptureClient,
		&wl.captureCli,
	); err != nil {
		return fmt.Errorf("获取捕获客户端失败: %w", err)
	}

	// 开始捕获
	if err := wl.audioClient.Start(); err != nil {
		wl.captureCli.Release()
		wl.captureCli = nil
		return fmt.Errorf("启动音频捕获失败: %w", err)
	}

	wl.started = true
	return nil
}

func (wl *wasapiLoopback) Read(data []byte) (int, error) {
	wl.mu.Lock()
	if wl.closed || !wl.started {
		wl.mu.Unlock()
		return 0, fmt.Errorf("捕获器未启动或已关闭")
	}
	wl.mu.Unlock()

	// 等待可用数据
	var framesAvailable uint32

	for {
		// 检查下一个包的大小
		if err := wl.captureCli.GetNextPacketSize(&framesAvailable); err != nil {
			return 0, fmt.Errorf("获取下一个包大小失败: %w", err)
		}

		if framesAvailable > 0 {
			break
		}

		// 短暂休眠，等待数据
		time.Sleep(time.Millisecond * 5)
	}

	// 读取数据
	var buffer *byte
	var framesRead uint32
	var flags uint32
	var devicePosition uint64
	var qpcPosition uint64

	if err := wl.captureCli.GetBuffer(
		&buffer,
		&framesRead,
		&flags,
		&devicePosition,
		&qpcPosition,
	); err != nil {
		return 0, fmt.Errorf("读取音频缓冲区失败: %w", err)
	}

	// AUDCLNT_BUFFERFLAGS_SILENT = 0x1，WASAPI 标记缓冲区为静音
	const AUDCLNT_BUFFERFLAGS_SILENT = 0x1
	if flags&AUDCLNT_BUFFERFLAGS_SILENT != 0 {
		wl.captureCli.ReleaseBuffer(framesRead)
		return 0, nil
	}

	// 计算数据大小
	dataSize := framesRead * wl.blockAlign

	// 检查是否有足够空间
	if len(data) < int(dataSize) {
		wl.captureCli.ReleaseBuffer(framesRead)
		return 0, fmt.Errorf("缓冲区太小: 需要 %d, 只有 %d", dataSize, len(data))
	}

	// 复制数据到输出缓冲区
	if buffer != nil && dataSize > 0 {
		src := unsafe.Slice(buffer, dataSize)
		copy(data, src)
	}

	// 释放缓冲区
	if err := wl.captureCli.ReleaseBuffer(framesRead); err != nil {
		return 0, fmt.Errorf("释放音频缓冲区失败: %w", err)
	}

	return int(dataSize), nil
}

func (wl *wasapiLoopback) Stop() error {
	wl.mu.Lock()
	defer wl.mu.Unlock()

	if !wl.started || wl.closed {
		return nil
	}

	if wl.audioClient != nil {
		wl.audioClient.Stop()
		wl.audioClient.Reset()
	}

	wl.started = false
	return nil
}

func (wl *wasapiLoopback) Close() error {
	wl.mu.Lock()
	defer wl.mu.Unlock()

	if wl.closed {
		return nil
	}
	wl.closed = true
	wl.started = false

	// 按顺序释放 COM 资源
	if wl.captureCli != nil {
		wl.captureCli.Release()
		wl.captureCli = nil
	}
	if wl.audioClient != nil {
		wl.audioClient.Release()
		wl.audioClient = nil
	}
	if wl.audioDevice != nil {
		wl.audioDevice.Release()
		wl.audioDevice = nil
	}
	if wl.deviceEnum != nil {
		wl.deviceEnum.Release()
		wl.deviceEnum = nil
	}

	// 反初始化 COM
	ole.CoUninitialize()

	// 解锁 OS 线程
	runtime.UnlockOSThread()

	return nil
}
