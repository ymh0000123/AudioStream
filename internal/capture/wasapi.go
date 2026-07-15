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
	"golang.org/x/sys/windows"

	"audiostream/internal/logx"
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
	eventHandle  windows.Handle
	eventDriven  bool
	started      bool
	closed       bool
	mu           sync.Mutex
}

func newLoopback() (Capture, error) {
	return &wasapiLoopback{}, nil
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

	// ★ 锁定 OS 线程：COM 要求所有操作在同一个线程上 ★
	// 此函数必须从将要调用 Read() 的 goroutine 中调用
	runtime.LockOSThread()

	// 初始化 COM
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("COM 初始化失败: %w", err)
	}
	logx.Debugf("wasapi", "WASAPI: COM 初始化成功")

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
		return fmt.Errorf("创建设备枚举器失败: %w", err)
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
		return fmt.Errorf("获取默认音频设备失败: %w", err)
	}
	logx.Debugf("wasapi", "WASAPI: 默认音频渲染设备已获取")

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
		return fmt.Errorf("激活音频客户端失败: %w", err)
	}

	// 4. 获取混音格式
	if err := wl.audioClient.GetMixFormat(&wl.mixFormat); err != nil {
		wl.audioClient.Release()
		wl.audioDevice.Release()
		wl.deviceEnum.Release()
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return fmt.Errorf("获取混音格式失败: %w", err)
	}

	// 解析音频格式
	wl.format = Format{
		SampleRate:    int(wl.mixFormat.NSamplesPerSec),
		Channels:      int(wl.mixFormat.NChannels),
		BitsPerSample: int(wl.mixFormat.WBitsPerSample),
	}

	wl.blockAlign = uint32(wl.mixFormat.NBlockAlign)
	logx.Debugf("wasapi", "WASAPI: 混音格式 SampleRate=%d, Channels=%d, BitsPerSample=%d, BlockAlign=%d",
		wl.format.SampleRate, wl.format.Channels, wl.format.BitsPerSample, wl.blockAlign)

	// 5. 初始化音频客户端（Loopback 模式）
	// 使用 REFERENCE_TIME 格式：100 纳秒为单位
	// 缓冲持续时间设为 0，使用默认值
	eventHandle, eventErr := windows.CreateEvent(nil, 0, 0, nil)
	if eventErr == nil {
		eventFlags := uint32(wca.AUDCLNT_STREAMFLAGS_LOOPBACK | wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK)
		if err := wl.initializeAudioClient(eventFlags); err == nil {
			wl.eventHandle = eventHandle
			if err := wl.audioClient.SetEventHandle(uintptr(eventHandle)); err != nil {
				wl.releaseStartupResources()
				return fmt.Errorf("设置 WASAPI 事件句柄失败: %w", err)
			}
			wl.eventDriven = true
			logx.Debugf("wasapi", "WASAPI: 已启用事件驱动捕获")
		} else {
			windows.CloseHandle(eventHandle)
			logx.Debugf("wasapi", "WASAPI: 事件驱动不可用 (%v), 回退到低间隔轮询", err)
		}
	} else {
		logx.Debugf("wasapi", "WASAPI: 创建事件失败 (%v), 回退到低间隔轮询", eventErr)
	}

	if !wl.eventDriven {
		if err := wl.initializeAudioClient(wca.AUDCLNT_STREAMFLAGS_LOOPBACK); err != nil {
			wl.releaseStartupResources()
			return fmt.Errorf("初始化音频客户端失败: %w", err)
		}
	}

	// 6. 获取缓冲区大小
	if err := wl.audioClient.GetBufferSize(&wl.bufferFrames); err != nil {
		wl.releaseStartupResources()
		return fmt.Errorf("获取缓冲区大小失败: %w", err)
	}
	logx.Debugf("wasapi", "WASAPI: 缓冲区大小 %d 帧", wl.bufferFrames)

	// 获取捕获客户端
	if err := wl.audioClient.GetService(
		wca.IID_IAudioCaptureClient,
		&wl.captureCli,
	); err != nil {
		wl.releaseStartupResources()
		return fmt.Errorf("获取捕获客户端失败: %w", err)
	}

	// 开始捕获
	if err := wl.audioClient.Start(); err != nil {
		wl.captureCli.Release()
		wl.captureCli = nil
		wl.releaseStartupResources()
		return fmt.Errorf("启动音频捕获失败: %w", err)
	}

	wl.started = true
	logx.Debugf("wasapi", "WASAPI: 捕获已启动")
	return nil
}

func (wl *wasapiLoopback) initializeAudioClient(streamFlags uint32) error {
	err := wl.audioClient.Initialize(
		wca.AUDCLNT_SHAREMODE_SHARED,
		streamFlags,
		0,
		0,
		wl.mixFormat,
		nil,
	)
	if err == nil {
		return nil
	}

	logx.Debugf("wasapi", "WASAPI: Initialize 失败: %v, 尝试 AUTOCONVERTPCM", err)
	err2 := wl.audioClient.Initialize(
		wca.AUDCLNT_SHAREMODE_SHARED,
		streamFlags|wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM,
		0,
		0,
		wl.mixFormat,
		nil,
	)
	if err2 != nil {
		return fmt.Errorf("AUTOCONVERTPCM: %w (首次: %v)", err2, err)
	}
	return nil
}

func (wl *wasapiLoopback) releaseStartupResources() {
	wl.closeEventHandle()
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
	if wl.mixFormat != nil {
		ole.CoTaskMemFree(uintptr(unsafe.Pointer(wl.mixFormat)))
		wl.mixFormat = nil
	}
	ole.CoUninitialize()
	runtime.UnlockOSThread()
}

func (wl *wasapiLoopback) closeEventHandle() {
	if wl.eventHandle != 0 {
		windows.CloseHandle(wl.eventHandle)
		wl.eventHandle = 0
	}
	wl.eventDriven = false
}

func (wl *wasapiLoopback) Read(data []byte) (int, error) {
	wl.mu.Lock()
	if wl.closed || !wl.started {
		wl.mu.Unlock()
		return 0, fmt.Errorf("捕获器未启动或已关闭")
	}
	wl.mu.Unlock()

	var framesAvailable uint32
	if wl.eventDriven {
		result, err := windows.WaitForSingleObject(wl.eventHandle, 100)
		if err != nil {
			return 0, fmt.Errorf("等待 WASAPI 事件失败: %w", err)
		}
		if result == uint32(windows.WAIT_TIMEOUT) {
			if err := wl.captureCli.GetNextPacketSize(&framesAvailable); err != nil {
				return 0, fmt.Errorf("获取下一个包大小失败: %w", err)
			}
			if framesAvailable == 0 {
				return 0, nil
			}
			wl.eventDriven = false
			logx.Debugf("wasapi", "WASAPI: 事件未触发但已有数据，切换为低间隔轮询")
		}
		if result != windows.WAIT_OBJECT_0 {
			return 0, fmt.Errorf("WASAPI 事件等待返回异常状态: 0x%X", result)
		}
	} else {
		deadline := time.Now().Add(100 * time.Millisecond)
		for {
			if err := wl.captureCli.GetNextPacketSize(&framesAvailable); err != nil {
				return 0, fmt.Errorf("获取下一个包大小失败: %w", err)
			}
			if framesAvailable > 0 {
				break
			}
			if time.Now().After(deadline) {
				return 0, nil
			}
			time.Sleep(time.Millisecond)
		}
	}

	if framesAvailable == 0 {
		if err := wl.captureCli.GetNextPacketSize(&framesAvailable); err != nil {
			return 0, fmt.Errorf("获取下一个包大小失败: %w", err)
		}
		if framesAvailable == 0 {
			return 0, nil
		}
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
		logx.Debugf("wasapi", "WASAPI: 静音缓冲区标志 (flags=0x%X)", flags)
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

	logx.Debugf("wasapi", "WASAPI: 停止捕获")
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
	logx.Debugf("wasapi", "WASAPI: 释放 COM 资源")

	// 按顺序释放 COM 资源
	if wl.captureCli != nil {
		wl.captureCli.Release()
		wl.captureCli = nil
	}
	wl.closeEventHandle()
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

	// 释放 mixFormat (CoTaskMemAlloc 分配的)
	if wl.mixFormat != nil {
		ole.CoTaskMemFree(uintptr(unsafe.Pointer(wl.mixFormat)))
		wl.mixFormat = nil
	}

	// 反初始化 COM
	ole.CoUninitialize()

	// 解锁 OS 线程
	// 注意：此函数必须从调用 Start() 的同一个 goroutine 中调用
	runtime.UnlockOSThread()

	return nil
}
