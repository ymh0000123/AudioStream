// AudioStream Server - 音频流发送端
// 捕获系统音频输出并通过 TCP 发送到客户端
// 同时支持 Web 浏览器播放（通过 WebSocket）
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	qr "github.com/skip2/go-qrcode"

	"audiostream/internal/capture"
	"audiostream/internal/silence"
	"audiostream/internal/transport"
	"audiostream/internal/webplayer"
)

var (
	addr      = flag.String("addr", ":19730", "TCP 监听地址 (默认 :19730)")
	webAddr   = flag.String("web", ":8080", "Web 播放器监听地址 (设为空禁用, 默认 :8080)")
	captureM  = flag.String("capture", "wasapi", "音频捕获后端: wasapi 或 ffmpeg (默认 wasapi)")
	device    = flag.String("device", "", "FFmpeg 音频设备名 (留空自动检测)")
	listDev   = flag.Bool("list-devices", false, "列出 FFmpeg 可用音频设备后退出")
	bufSize   = flag.Int("buf", 65536, "音频缓冲区大小 (默认 65536)")
)

func main() {
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[AudioStream Server] ")

	fmt.Println(`
  ╔══════════════════════════════════════════╗
  ║       AudioStream Server v1.0            ║
  ║   跨平台电脑音频传输工具 - 发送端         ║
  ╚══════════════════════════════════════════╝
	`)

	// ========== 列出 FFmpeg 设备（如果请求）==========
	if *listDev {
		listFFmpegDevices()
		return
	}

	// ========== 初始化音频捕获 ==========
	captureType := strings.ToLower(*captureM)
	var cap capture.Capture
	var err error

	switch captureType {
	case "ffmpeg":
		if !capture.FFmpegAvailable() {
			log.Fatalf("❌ %v", capture.ErrFFmpegNotFound)
		}
		log.Printf("正在初始化 FFmpeg 音频捕获 (设备: %s)...",
			func() string { if *device == "" { return "自动检测" }; return *device }())
		cap, err = capture.NewFFmpeg(*device)
		if err != nil {
			log.Fatalf("FFmpeg 音频捕获初始化失败: %v", err)
		}
		log.Println("✅ FFmpeg 音频捕获已初始化")

	case "wasapi":
		fallthrough
	default:
		log.Println("正在初始化 WASAPI Loopback 音频捕获...")
		cap, err = capture.NewLoopback()
		if err != nil {
			log.Fatalf("WASAPI 音频捕获初始化失败: %v", err)
		}
	}

	defer cap.Close()

	// 获取音频格式
	audioFormat := cap.Format()
	log.Printf("音频格式: %s", audioFormat)

	// 启动捕获
	if err := cap.Start(); err != nil {
		log.Fatalf("音频捕获启动失败: %v", err)
	}
	log.Println("✅ 音频捕获已启动")

	// ========== 初始化 Web 播放器（如果启用）==========
	var webHub *webplayer.Hub
	if *webAddr != "" {
		webHub = webplayer.NewHub(audioFormat)
		go func() {
			if err := webHub.StartHTTPServer(*webAddr); err != nil {
				log.Printf("[WebPlayer] ❌ HTTP 服务启动失败: %v", err)
			}
		}()
	}

	// ========== 初始化网络服务端 ==========
	serverFormat := transport.AudioFormat{
		SampleRate:    audioFormat.SampleRate,
		Channels:      audioFormat.Channels,
		BitsPerSample: audioFormat.BitsPerSample,
	}

	server := transport.NewServer(*addr, serverFormat)
	if err := server.Start(); err != nil {
		log.Fatalf("服务端启动失败: %v", err)
	}
	defer server.Close()

	log.Printf("📡 TCP 服务端已启动，监听地址: %s", server.Addr())

	// ========== 显示二维码 ==========
	if *webAddr != "" {
		_, port, _ := net.SplitHostPort(*webAddr)
		if port == "" {
			port = "8080"
		}
		ip := localIP()
		if ip != "" {
			webURL := fmt.Sprintf("http://%s:%s", ip, port)
			fmt.Println()
			fmt.Println("📱 扫码打开 Web 播放器:")
			fmt.Println()
			png, _ := qr.New(webURL, qr.Low)
			fmt.Print(png.ToSmallString(false))
			fmt.Println()
			fmt.Printf("   %s\n", webURL)
			fmt.Println()
		}
	}

	// ========== 设置信号处理 ==========
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// ========== 音频捕获通道（独立于 TCP 连接）==========
	audioData := make(chan []byte, 128)
	done := make(chan struct{})
	running := true

	// 启动后台捕获循环：立即开始捕获并广播到 Web 端
	// TCP 客户端连接后也从同一通道获取数据
	go func() {
		buf := make([]byte, *bufSize)
		readCount := 0
		silentCount := 0
		for {
			select {
			case <-done:
				return
			default:
			}

			n, err := cap.Read(buf)
			if err != nil {
				log.Printf("读取音频数据失败: %v", err)
				continue
			}
			if n == 0 {
				silentCount++
				continue
			}

			// 静音检测：幅度低于阈值时跳过发送
			if silence.IsSilent(buf[:n], audioFormat.BitsPerSample) {
				silentCount++
				continue
			}

			// 复制数据（通道和 Web 广播需要各自的副本）
			data := make([]byte, n)
			copy(data, buf[:n])

			// 优先广播到 Web 浏览器端（不依赖 TCP 连接）
			if webHub != nil {
				webHub.Broadcast(data)
			}

			// 推送到 TCP 通道（非阻塞，避免堵塞捕获循环）
			select {
			case audioData <- data:
			default:
				// 通道满了说明 TCP 客户端消费太慢或未连接，丢弃
			}

			// 打印心跳日志（每 300 次读取约 3 秒）
			readCount++
			if webHub != nil && readCount%300 == 0 {
				log.Printf("🎵 音频捕获中: 已读取 %d 包 (静音跳过 %d), Web 客户端: %d",
					readCount, silentCount, webHub.ClientCount())
				silentCount = 0
			}
		}
	}()

	// ========== 等待 TCP 连接（可选）==========
	log.Println("等待 TCP 客户端连接（可选，Web 播放不依赖于此）...")
	sender, err := server.Accept()
	if err != nil {
		log.Printf("TCP 客户端接受失败（Web 播放不受影响）: %v", err)
		// 如果没有 TCP 客户端，继续运行（Web 依然在工作）
	}
	if sender != nil {
		defer sender.Close()
		log.Printf("🔗 TCP 客户端已连接: %s", sender.RemoteAddr())

		if err := sender.Handshake(); err != nil {
			log.Printf("TCP 握手失败: %v（Web 播放不受影响）", err)
		} else {
			log.Println("🤝 TCP 握手完成")
		}
	}

	// ========== 传输循环 ==========
	sentBytes := int64(0)
	packets := int64(0)

	if sender != nil {
		log.Println("▶️  音频流传输中... (按 Ctrl+C 停止)")
	} else {
		log.Println("▶️  Web 播放模式已启动 (按 Ctrl+C 停止)")
	}
	fmt.Println()

	for running {
		select {
		case <-sigCh:
			log.Println("收到停止信号")
			running = false
		case data, ok := <-audioData:
			if !ok {
				running = false
				break
			}

			// 发送到 TCP 客户端（如果有）
			if sender != nil {
				if err := sender.SendFrame(data); err != nil {
					log.Printf("TCP 发送失败（Web 播放继续）: %v", err)
					sender.Close()
					sender = nil
					log.Println("TCP 客户端已断开，继续 Web 播放模式")
				}
			}

			sentBytes += int64(len(data))
			packets++

			// 每 5 秒打印一次状态
			if packets%300 == 0 {
				log.Printf("已发送: %d 包 / %.2f MB (Web 客户端: %d)",
					packets, float64(sentBytes)/1024/1024, webHubCount(webHub))
			}
		}
	}

	// 发送剩余的累积数据
	if webHub != nil {
		webHub.Flush()
	}

	// ========== 清理 ==========
	log.Println()
	log.Println("正在停止...")
	close(done)
	cap.Stop()

	if webHub != nil {
		webHub.Stop()
	}

	log.Printf("📊 统计: 共发送 %d 包 / %.2f MB", packets, float64(sentBytes)/1024/1024)
	log.Println("👋 服务端已关闭")
}

// webHubCount 安全获取 Web 客户端数量
func webHubCount(h *webplayer.Hub) int {
	if h == nil {
		return 0
	}
	return h.ClientCount()
}

// localIP 获取本机局域网 IPv4 地址，优先选择常见内网网段
func localIP() string {
	var fallback string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ip := ipNet.IP.To4()
			// 优先 192.168.x.x
			if ip[0] == 192 && ip[1] == 168 {
				return ip.String()
			}
			// 其次 10.x.x.x
			if ip[0] == 10 {
				if fallback == "" {
					fallback = ip.String()
				}
			}
			// 再次 172.16-31.x.x
			if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
				if fallback == "" {
					fallback = ip.String()
				}
			}
			// 其他地址作为兜底
			if fallback == "" {
				fallback = ip.String()
			}
		}
	}
	return fallback
}

// listFFmpegDevices 列出可用的 FFmpeg 音频设备
func listFFmpegDevices() {
	if !capture.FFmpegAvailable() {
		log.Fatalf("❌ %v", capture.ErrFFmpegNotFound)
	}

	log.Println("正在扫描 FFmpeg 可用音频设备...")
	devices, err := capture.ListFFmpegDevices()
	if err != nil {
		log.Fatalf("扫描失败: %v", err)
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Println("  FFmpeg 可用的音频输入设备:")
	fmt.Println("═══════════════════════════════════════")
	for i, dev := range devices {
		fmt.Printf("  %2d. %s\n", i+1, dev)
	}
	fmt.Println("═══════════════════════════════════════")
	fmt.Println()
	fmt.Println("使用示例: server.exe -capture ffmpeg -device \"立体声混音 (Realtek High Definition Audio)\"")
	fmt.Println()
}
