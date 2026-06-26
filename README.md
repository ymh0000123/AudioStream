# AudioStream - 跨平台电脑音频传输工具

将一台电脑的系统音频输出，通过网络实时传输到另一台电脑播放。

## 功能特点

- 🎵 **实时传输**：低延迟传输电脑系统音频（扬声器输出）
- 🔌 **即插即用**：无需配置，一条命令启动服务端，一条命令启动客户端
- 🪟 **Windows 原生支持**：使用 WASAPI Loopback 捕获系统音频
- 📦 **纯 Go 实现**：编译为单个可执行文件，无需外部依赖
- 🔊 **高音质**：原生 48kHz 16bit 立体声 PCM 传输

## 工作原理

```
┌────────────────────┐          TCP 连接          ┌────────────────────┐
│  发送端 (Server)    │◄─────────────────────────►│  接收端 (Client)    │
│                    │          :19730            │                    │
│  ┌──────────────┐  │                           │  ┌──────────────┐  │
│  │ WASAPI       │  │   PCM 音频流               │  │ oto 音频播放 │  │
│  │ Loopback 捕获 │──┼──────────────────────────▶│  │              │  │
│  └──────────────┘  │   握手 → 格式协商 → 传输    │  └──────────────┘  │
│                    │                           │                    │
└────────────────────┘                           └────────────────────┘
```

## 使用场景

- 🎮 将游戏电脑的音频传输到另一台电脑/手机
- 🎬 将远程会议音频转发到更好的音响设备
- 🖥️ 将主机音频传输到客户端电脑

## 系统要求

| 组件 | 要求 |
|------|------|
| **发送端** | Windows 10/11（需要 WASAPI 支持） |
| **接收端** | Windows / macOS / Linux（使用 oto 播放） |
| **网络** | 局域网或低延迟网络连接 |
| **Go 版本** | 1.21+（仅编译时需要） |

> **注意**：音频捕获功能目前仅在 Windows 上支持（WASAPI Loopback）。
> 接收端（播放）跨平台支持 Windows、macOS、Linux。

## 快速开始

### 方式一：下载预编译版本

从 [Releases](../../releases) 页面下载对应平台的压缩包，解压后直接运行。

### 方式二：从源码编译

```bash
# 确保已安装 Go 1.21+
go version

# 克隆项目
git clone https://github.com/yourusername/audiostream.git
cd audiostream

# 编译
go build -o server.exe ./cmd/server
go build -o client.exe ./cmd/client

# 或者使用构建脚本
.\build.ps1
```

### 使用教程

#### 1️⃣ 在音频源电脑上启动发送端（Server）

```bash
# 默认监听 :19730
server.exe

# 指定监听地址和端口
server.exe -addr :19731
```

成功启动后，终端会显示：
```
[AudioStream Server] 正在初始化 WASAPI Loopback 音频捕获...
[AudioStream Server] 音频格式: 48000Hz 2ch 16bit PCM
[AudioStream Server] ✅ 音频捕获已启动
[AudioStream Server] 📡 服务端已启动，监听地址: 0.0.0.0:19730
[AudioStream Server] 等待客户端连接...
```

> 💡 **提示**：如果遇到防火墙提示，请允许程序通过防火墙。

#### 2️⃣ 在接收端电脑上启动接收端（Client）

```bash
# 连接服务端
client.exe -addr 192.168.1.100:19730

# 如果不指定地址，默认连接 127.0.0.1:19730（本机测试）
client.exe
```

成功连接后，终端会显示：
```
[AudioStream Client] 正在连接服务端 192.168.1.100:19730 ...
[AudioStream Client] ✅ 已连接服务端
[AudioStream Client] 接收到音频格式: 48000Hz 2ch 16bit
[AudioStream Client] ✅ 音频播放器已启动
[AudioStream Client] ▶️  正在接收音频流... (按 Ctrl+C 停止)
```

#### 3️⃣ 停止传输

在任意端按 `Ctrl + C` 即可停止。

## 命令行参数

### Server（发送端）

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `:19730` | 监听地址和端口 |
| `-buf` | `65536` | 音频缓冲区大小（字节） |

### Client（接收端）

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `127.0.0.1:19730` | 服务端地址和端口 |

## 项目结构

```
audiostream/
├── cmd/
│   ├── server/            # 发送端入口
│   │   └── main.go
│   └── client/            # 接收端入口
│       └── main.go
├── internal/
│   ├── capture/           # 音频捕获模块
│   │   ├── capture.go     # 接口定义
│   │   ├── wasapi.go      # Windows WASAPI 实现
│   │   └── capture_stub.go # 非 Windows 桩实现
│   ├── player/            # 音频播放模块
│   │   └── player.go
│   └── transport/         # 网络传输模块
│       └── transport.go
├── go.mod
├── go.sum
├── build.ps1              # 构建脚本
└── README.md
```

## 技术细节

### 音频格式
- **采样率**：取决于系统混音器设置（通常 48000Hz）
- **位深**：16 位
- **通道**：立体声（2 通道）
- **编码**：原始 PCM（未压缩）
- **传输协议**：TCP，先发送 4 字节长度前缀，后跟 PCM 数据

### 带宽估算
- 48000Hz × 2ch × 16bit = 约 1.5 Mbps
- 局域网环境完全足够

## 常见问题

### Q: 客户端无法连接到服务端？
**A**: 请检查：
1. 服务端防火墙是否放行端口
2. 两台电脑是否在同一网络
3. 地址和端口是否输入正确

### Q: 音频有卡顿或噪音？
**A**: 尝试：
1. 使用有线网络替代 Wi-Fi
2. 增加缓冲区大小：`server.exe -buf 131072`
3. 检查网络延迟

### Q: 支持 macOS 或 Linux 作为发送端吗？
**A**: 目前音频捕获仅在 Windows 上实现（WASAPI Loopback）。
macOS 和 Linux 的音频捕获支持正在计划中。

### Q: 支持多客户端同时接收吗？
**A**: 当前版本仅支持一对一的传输。多播功能在计划中。

## 开发计划

- [ ] macOS CoreAudio 音频捕获支持
- [ ] Linux PulseAudio 音频捕获支持
- [ ] Opus 音频压缩编码
- [ ] 多客户端同时传输
- [ ] WebRTC 支持
- [ ] 图形界面

## 许可证

MIT License
