# AudioStream

实时捕获 Windows 系统音频，通过 WebSocket 推送到浏览器播放。无需安装客户端，手机扫码即用。

## 工作原理

```
┌─── Server (Windows) ──────────────────────────┐
│                                               │
│  WASAPI / FFmpeg 捕获                          │
│       │                                       │
│       ▼                                       │
│  静音检测 → 32→16bit 转换 → WebSocket 广播     │
│                                    │          │
│                          HTTP :8080           │
└────────────────────────────────────┼──────────┘
                                     │
            ┌────────────────────────┼──────────────┐
            │        浏览器 (任意设备)                │
            │  AudioContext + WebSocket 播放         │
            │  媒体控制 / 码率切换 / 播放状态显示     │
            └───────────────────────────────────────┘
```

## 功能

- WASAPI Loopback 捕获系统音频（免驱动，原生低延迟）
- FFmpeg 后端（DirectShow / WASAPI / PulseAudio）
- 浏览器 Web 播放器，无需安装任何客户端
- 二维码扫码访问，mDNS 自动发现
- 32-bit float → 16-bit int 实时转换
- 多码率适配（128 ~ 1024 kbps）
- 媒体键远程控制（播放/暂停/上一曲/下一曲）
- SMTC 集成，显示歌曲标题、进度、专辑信息
- 静音检测，跳过无声片段减少带宽

## 快速开始

### 编译

需要 Go 1.24+ 和 CGO 环境（MinGW-w64）。

```powershell
# 方式一：使用构建脚本（推荐）
.\build.ps1

# 方式二：手动编译
$env:CGO_ENABLED='1'
go build -ldflags="-s -w" -o server.exe ./cmd/server
```

> `smtc.dll` 用于 SMTC 媒体状态查询。如果未安装 Visual Studio Build Tools，构建脚本会跳过 DLL 编译，SMTC 功能降级为不可用，不影响音频传输。

### 运行

```powershell
# 启动服务端（默认 WASAPI + Web 播放器 :8080）
.\server.exe

# 使用 FFmpeg 后端
.\server.exe -capture ffmpeg

# 指定 FFmpeg 设备
.\server.exe -capture ffmpeg -device "立体声混音 (Realtek High Definition Audio)"

# 禁用 Web 播放器
.\server.exe -web ""

# 列出 FFmpeg 可用设备
.\server.exe -list-devices
```

启动后终端会显示二维码，用手机扫码即可在浏览器中播放系统音频。

## 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-web` | `:8080` | Web 播放器监听地址（空字符串禁用） |
| `-capture` | `wasapi` | 音频捕获后端：`wasapi` 或 `ffmpeg` |
| `-device` | `""` | FFmpeg 音频设备名（留空自动检测） |
| `-list-devices` | `false` | 列出 FFmpeg 可用音频设备并退出 |
| `-buf` | `65536` | 音频缓冲区大小（字节） |
| `-log` | `""` | 调试日志类别，逗号分隔：`wasapi,ffmpeg,webplayer,media,capture` 或 `all` |

## 项目结构

```
cmd/server/
  main.go                  # 入口：标志解析、捕获初始化、Web 服务、信号处理

internal/capture/
  capture.go               # Capture 接口定义
  wasapi.go                # Windows WASAPI Loopback 实现
  ffmpeg.go                # FFmpeg 子进程捕获
  capture_stub.go          # 非 Windows 桩实现

internal/webplayer/
  webplayer.go             # WebSocket Hub：广播、格式转换、HTTP 服务
  player.html              # 嵌入式 Web UI（embed.FS）
  command.go               # 媒体命令解析
  mediakeys.go             # Windows 媒体键模拟（SendInput）
  mediastate.go            # 播放状态轮询（SMTC + 音频能量检测）
  mediastate_dll.go        # SMTC C++/WinRT DLL 查询
  mediastate_stub.go       # 非 Windows 桩实现
  mediaseek.go             # 进度条拖动
  audiosession.go          # Windows 音频会话查询

internal/silence/
  silence.go               # 静音检测（int16 / float32）

internal/logx/
  logx.go                  # 分类调试日志

build.ps1                  # Windows 构建脚本
```

## WebSocket 协议

连接 `/ws` 端点后：

1. 服务端发送 JSON 格式信息：`{"type":"format","sample_rate":48000,"channels":2,"bits_per_sample":16}`
2. 服务端推送二进制 16-bit PCM 音频帧（约 50ms 一批）
3. 服务端定期广播播放状态：`{"type":"state","playing":true,"title":"...","position":...,"duration":...}`
4. 客户端可发送命令：
   - `play_pause` — 播放/暂停
   - `previous` / `next` — 上一曲/下一曲
   - `seek_to` — 跳转到指定位置（毫秒）
   - `set_volume` — 设置音量
   - `set_bitrate` — 切换码率（128/256/512/1024 kbps）
   - `get_state` — 请求当前播放状态

## 系统要求

| 组件 | 要求 |
|------|------|
| 操作系统 | Windows 10/11（音频捕获） |
| 浏览器 | Chrome / Edge / Firefox / Safari（播放端） |
| Go | 1.24+（仅编译时） |
| CGO | 需要 GCC/Clang（MinGW-w64） |
| FFmpeg | 可选，仅 `-capture ffmpeg` 时需要 |
| VS Build Tools | 可选，仅编译 `smtc.dll` 时需要 |

## 许可证

MIT License
