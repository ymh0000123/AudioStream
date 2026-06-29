//go:build !stub
// +build !stub

package capture

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"audiostream/internal/logx"
)

// ffmpegCapture 使用 FFmpeg 子进程捕获系统音频
type ffmpegCapture struct {
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	stderr   bytes.Buffer
	format   Format
	devName  string
	started  bool
	closed   bool
	readBuf  []byte
	readPos  int
	readSize int
	mu       sync.Mutex
	doneCh   chan struct{}
}

// ListFFmpegDevices 列出 FFmpeg 可用的音频输入设备
func ListFFmpegDevices() ([]string, error) {
	// 尝试多种 FFmpeg 输入格式找到可用的音频设备
	var devices []string

	// 1. 尝试 DirectShow (Windows)
	devs, err := listDShowDevices()
	if err == nil && len(devs) > 0 {
		devices = append(devices, devs...)
	}

	// 2. 尝试 WASAPI (Windows)
	devs, err = listWASAPIDevices()
	if err == nil && len(devs) > 0 {
		devices = append(devices, devs...)
	}

	// 3. 尝试 PulseAudio (Linux)
	devs, err = listPulseDevices()
	if err == nil && len(devs) > 0 {
		devices = append(devices, devs...)
	}

	if len(devices) == 0 {
		return nil, fmt.Errorf("未找到 FFmpeg 可用的音频设备")
	}
	return devices, nil
}

// listDShowDevices 列出 DirectShow 音频设备
func listDShowDevices() ([]string, error) {
	cmd := exec.Command("ffmpeg", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Run() // 预期会失败，但 stderr 中有设备列表

	var devices []string
	re := regexp.MustCompile(`"([^"]+)"\s+\(audio\)`)
	matches := re.FindAllStringSubmatch(stderr.String(), -1)
	for _, m := range matches {
		devices = append(devices, m[1])
	}
	return devices, nil
}

// listWASAPIDevices 列出 WASAPI 音频设备
func listWASAPIDevices() ([]string, error) {
	cmd := exec.Command("ffmpeg", "-list_devices", "true", "-f", "wasapi", "-i", "dummy")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Run() // 预期会失败

	var devices []string
	re := regexp.MustCompile(`"([^"]+)"\s+\(audio\)`)
	matches := re.FindAllStringSubmatch(stderr.String(), -1)
	for _, m := range matches {
		devices = append(devices, m[1])
	}
	return devices, nil
}

// listPulseDevices 列出 PulseAudio 设备
func listPulseDevices() ([]string, error) {
	cmd := exec.Command("ffmpeg", "-sources", "pulse", "-f", "pulse", "-i", "dummy")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Run()

	var devices []string
	re := regexp.MustCompile(`"([^"]+)"`)
	matches := re.FindAllStringSubmatch(stderr.String(), -1)
	for _, m := range matches {
		devices = append(devices, m[1])
	}
	return devices, nil
}

// findStereoMix 在设备列表中查找立体声混音或扬声器设备
func findStereoMix(devices []string) string {
	// 优先找"立体声混音"、"Stereo Mix"
	preferred := []string{"立体声混音", "Stereo Mix", "stereo", "mix"}
	for _, dev := range devices {
		lower := strings.ToLower(dev)
		for _, pref := range preferred {
			if strings.Contains(lower, strings.ToLower(pref)) {
				return dev
			}
		}
	}
	// 其次找麦克风或输入设备
	if len(devices) > 0 {
		return devices[0]
	}
	return ""
}

// NewFFmpeg 创建 FFmpeg 音频捕获实例
// 如果 deviceName 为空，自动查找最佳设备
func NewFFmpeg(deviceName string) (Capture, error) {
	// 如果没有指定设备名，自动检测
	if deviceName == "" {
		devices, err := ListFFmpegDevices()
		if err != nil {
			return nil, fmt.Errorf("FFmpeg 自动检测设备失败: %w", err)
		}
		logx.Debugf("ffmpeg", "FFmpeg: 自动检测到 %d 个设备", len(devices))
		deviceName = findStereoMix(devices)
		if deviceName == "" {
			return nil, fmt.Errorf("未找到可用的音频设备，可用设备: %s", strings.Join(devices, ", "))
		}
		logx.Debugf("ffmpeg", "FFmpeg: 自动选择设备 %q", deviceName)
	}

	// 构建 FFmpeg 命令
	// 输出格式: 16-bit 立体声 PCM, 48000Hz
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-f", "dshow",
		"-i", "audio=" + deviceName,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ar", "48000",
		"-ac", "2",
		"-",
	}

	// 尝试用 PulseAudio
	cmd := exec.Command("ffmpeg", args...)
	logx.Debugf("ffmpeg", "FFmpeg: 命令行 ffmpeg %s", strings.Join(args, " "))

	return &ffmpegCapture{
		format: Format{
			SampleRate:    48000,
			Channels:      2,
			BitsPerSample: 16,
		},
		devName: deviceName,
		cmd:     cmd,
		doneCh:  make(chan struct{}),
	}, nil
}

func (fc *ffmpegCapture) Format() Format {
	return fc.format
}

func (fc *ffmpegCapture) Start() error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if fc.closed {
		return fmt.Errorf("捕获器已关闭")
	}
	if fc.started {
		return nil
	}

	// 启动 FFmpeg 子进程
	stdout, err := fc.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout pipe 失败: %w", err)
	}
	fc.cmd.Stderr = &fc.stderr

	if err := fc.cmd.Start(); err != nil {
		return fmt.Errorf("启动 FFmpeg 失败: %w", err)
	}

	fc.stdout = stdout
	fc.started = true
	logx.Debugf("ffmpeg", "FFmpeg: 子进程已启动 (PID: %d)", fc.cmd.Process.Pid)

	// 检查 FFmpeg 是否立即退出
	done := make(chan struct{}, 1)
	go func() {
		fc.cmd.Wait()
		close(fc.doneCh)
		done <- struct{}{}
	}()

	// 短暂等待检查 FFmpeg 是否崩溃
	select {
	case <-done:
		stderrStr := fc.stderr.String()
		if stderrStr != "" {
			logx.Debugf("ffmpeg", "FFmpeg: stderr 输出: %s", stderrStr)
			return fmt.Errorf("FFmpeg 启动失败: %s", stderrStr)
		}
		return fmt.Errorf("FFmpeg 启动后立即退出")
	default:
		logx.Debugf("ffmpeg", "FFmpeg: 启动检查通过")
	}

	return nil
}

func (fc *ffmpegCapture) Read(data []byte) (int, error) {
	fc.mu.Lock()
	started := fc.started
	closed := fc.closed
	fc.mu.Unlock()

	if !started {
		return 0, fmt.Errorf("捕获器未启动")
	}
	if closed {
		return 0, io.EOF
	}

	return fc.stdout.Read(data)
}

func (fc *ffmpegCapture) Stop() error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if !fc.started || fc.closed {
		return nil
	}

	// 终止 FFmpeg 进程
	if fc.cmd != nil && fc.cmd.Process != nil {
		logx.Debugf("ffmpeg", "FFmpeg: 终止子进程 (PID: %d)", fc.cmd.Process.Pid)
		fc.cmd.Process.Kill()
	}

	fc.started = false
	return nil
}

func (fc *ffmpegCapture) Close() error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if fc.closed {
		return nil
	}
	fc.closed = true

	if fc.cmd != nil && fc.cmd.Process != nil {
		fc.cmd.Process.Kill()
		// 等待进程结束
		<-fc.doneCh
	}

	return nil
}

// DeviceName 返回当前使用的音频设备名
func (fc *ffmpegCapture) DeviceName() string {
	return fc.devName
}
