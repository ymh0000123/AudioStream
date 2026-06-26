// Package capture 提供音频捕获功能
// 支持 Windows WASAPI Loopback 和 FFmpeg 子进程两种方式捕获系统音频
package capture

import (
	"fmt"
	"os/exec"
)

// Format 音频捕获格式
type Format struct {
	SampleRate    int // 采样率 (Hz)
	Channels      int // 通道数
	BitsPerSample int // 位深
}

// BytesPerFrame 返回每帧字节数
func (f Format) BytesPerFrame() int {
	return f.Channels * f.BitsPerSample / 8
}

// String 返回格式描述
func (f Format) String() string {
	return fmt.Sprintf("%dHz %dch %dbit PCM",
		f.SampleRate, f.Channels, f.BitsPerSample)
}

// Capture 音频捕获接口
type Capture interface {
	// Format 返回音频格式
	Format() Format

	// Start 开始捕获音频
	Start() error

	// Read 读取音频数据，data 是预分配的缓冲，返回实际读取的字节数
	Read(data []byte) (int, error)

	// Stop 停止捕获
	Stop() error

	// Close 释放资源
	Close() error
}

// NewLoopback 创建系统音频输出捕获实例（跨平台工厂函数）
// 在 Windows 上使用 WASAPI Loopback 捕获扬声器输出
// 在其他平台上返回 ErrUnsupportedPlatform
func NewLoopback() (Capture, error) {
	return newLoopback()
}

// ErrUnsupportedPlatform 不支持当前平台
var ErrUnsupportedPlatform = fmt.Errorf("当前平台暂不支持音频捕获，请使用 Windows 系统")

// FFmpegAvailable 检查系统是否安装了 FFmpeg
func FFmpegAvailable() bool {
	cmd := exec.Command("ffmpeg", "-version")
	return cmd.Run() == nil
}

// ErrFFmpegNotFound FFmpeg 未安装
var ErrFFmpegNotFound = fmt.Errorf("未找到 FFmpeg，请安装 FFmpeg 并确保其在 PATH 中")
