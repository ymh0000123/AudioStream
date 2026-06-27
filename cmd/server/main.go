// AudioStream Server - 音频流发送端
// 捕获系统音频输出并通过 WebSocket 传输到浏览器播放
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
	"github.com/grandcat/zeroconf"

	"audiostream/internal/capture"
	"audiostream/internal/silence"
	"audiostream/internal/webplayer"
)

var (
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

	// ========== 注册 mDNS 服务 ==========
	if *webAddr != "" {
		_, webPort, _ := net.SplitHostPort(*webAddr)
		if webPort == "" {
			webPort = "8080"
		}
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "AudioStream"
		}
		txtRecords := []string{
			fmt.Sprintf("sample_rate=%d", audioFormat.SampleRate),
			fmt.Sprintf("channels=%d", audioFormat.Channels),
			fmt.Sprintf("bits=%d", audioFormat.BitsPerSample),
		}
		mdnsServer, mdnsErr := zeroconf.Register(
			hostname,
			"_audiostream._tcp",
			"local.",
			portInt(webPort),
			txtRecords,
			nil,
		)
		if mdnsErr != nil {
			log.Printf("⚠️  mDNS 注册失败: %v (不影响正常功能)", mdnsErr)
		} else {
			log.Printf("🔍 mDNS 服务已注册: %s._audiostream._tcp", hostname)
			defer mdnsServer.Shutdown()
		}
	}

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

	// ========== 音频捕获循环 ==========
	done := make(chan struct{})
	sentBytes := int64(0)
	packets := int64(0)

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

			// 复制数据（Web 广播需要副本）
			data := make([]byte, n)
			copy(data, buf[:n])

			// 广播到 Web 浏览器端
			if webHub != nil {
				webHub.Broadcast(data)
			}

			sentBytes += int64(len(data))
			packets++

			// 打印心跳日志（每 300 次读取约 3 秒）
			readCount++
			if webHub != nil && readCount%300 == 0 {
				log.Printf("🎵 音频捕获中: 已读取 %d 包 (静音跳过 %d), Web 客户端: %d",
					readCount, silentCount, webHub.ClientCount())
				silentCount = 0
			}
		}
	}()

	log.Println("▶️  Web 播放模式已启动 (按 Ctrl+C 停止)")
	fmt.Println()

	// ========== 等待停止信号 ==========
	<-sigCh
	log.Println("收到停止信号")

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

// portInt 将端口字符串转换为整数
func portInt(port string) int {
	var n int
	fmt.Sscanf(port, "%d", &n)
	return n
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
