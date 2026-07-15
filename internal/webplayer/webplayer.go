// Package webplayer 提供 Web 浏览器端的音频播放功能
// 通过 WebSocket 将 PCM 音频推送到浏览器，使用 AudioContext 播放
package webplayer

import (
	"context"
	"embed"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"audiostream/internal/capture"
	"audiostream/internal/logx"
	"audiostream/internal/silence"
)

//go:embed player.html icon.svg
var pageContent embed.FS

const (
	audioPacketMilliseconds = 20
	clientAudioQueueSize    = 4
	clientControlQueueSize  = 8
	clientWriteTimeout      = 2 * time.Second
)

type outboundMessage struct {
	messageType int
	data        []byte
}

type clientConnection struct {
	conn      *websocket.Conn
	audio     chan []byte
	control   chan outboundMessage
	done      chan struct{}
	closeOnce sync.Once
}

// Hub 管理 WebSocket 连接并将音频数据广播给所有连接的浏览器
type Hub struct {
	clients   map[*websocket.Conn]*clientConnection
	mu        sync.RWMutex
	format    capture.Format
	webFormat capture.Format // 发送给浏览器的格式（始终为 16-bit）
	upgrader  websocket.Upgrader
	server    *http.Server
	addr      string
	accumBuf  []byte // 累积缓冲区，减少发送频率
	accMu     sync.Mutex
	convBuf   []byte // 格式转换缓冲区

	clientFormats map[*websocket.Conn]capture.Format // 每个客户端独立请求的目标格式
	clientFmtMu   sync.RWMutex

	lastNonSilentUnixNano atomic.Int64 // 最近一次非静音数据经过 Broadcast 的时间
	manuallyPaused        atomic.Bool  // 客户端通过浏览器手动暂停，覆盖音频检测
	ctx                   context.Context
	cancel                context.CancelFunc
}

// NewHub 创建新的 Web 播放中心
func NewHub(format capture.Format) *Hub {
	// Web 端始终用 16-bit（浏览器 AudioContext 原生支持良好）
	webFmt := capture.Format{
		SampleRate:    format.SampleRate,
		Channels:      format.Channels,
		BitsPerSample: 16,
	}
	logx.Debugf("webplayer", "WebPlayer: 源格式 %s, 浏览器格式 %s", format, webFmt)

	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		clients:       make(map[*websocket.Conn]*clientConnection),
		clientFormats: make(map[*websocket.Conn]capture.Format),
		format:        format,
		webFormat:     webFmt,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // 允许跨域连接
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 65536,
		},
		addr:   ":8080",
		ctx:    ctx,
		cancel: cancel,
	}
}

// Ctx 返回用于控制轮询goroutine的context
func (h *Hub) Ctx() context.Context {
	return h.ctx
}

// Broadcast 向所有连接的 WebSocket 客户端广播音频数据
func (h *Hub) Broadcast(data []byte) {
	h.mu.RLock()
	hasClients := len(h.clients) > 0
	h.mu.RUnlock()
	if !hasClients {
		return
	}

	// 静音检测：跳过静音数据的转换和广播
	if silence.IsSilent(data, h.format.BitsPerSample) {
		return
	}

	h.lastNonSilentUnixNano.Store(time.Now().UnixNano())

	// 如果源数据是 32-bit float，转换为 16-bit int
	if h.format.BitsPerSample == 32 {
		logx.Debugf("webplayer", "WebPlayer: 32-bit → 16-bit 转换 %d 字节", len(data))
		data = h.convert32bitTo16bit(data)
	}

	// 固定按 20ms 分包，在低延迟和调度稳定性之间取平衡。
	h.accMu.Lock()
	h.accumBuf = append(h.accumBuf, data...)
	accLen := len(h.accumBuf)
	targetSize := audioPacketSize(h.webFormat)
	logx.Debugf("webplayer", "WebPlayer: 累积缓冲 %d/%d 字节 (目标 %d)", accLen, cap(h.accumBuf), targetSize)
	if targetSize <= 0 || accLen < targetSize {
		h.accMu.Unlock()
		return
	}

	packetCount := accLen / targetSize
	packets := make([][]byte, packetCount)
	for i := range packets {
		packets[i] = make([]byte, targetSize)
		copy(packets[i], h.accumBuf[i*targetSize:(i+1)*targetSize])
	}
	remaining := accLen - packetCount*targetSize
	copy(h.accumBuf[:remaining], h.accumBuf[packetCount*targetSize:])
	h.accumBuf = h.accumBuf[:remaining]
	h.accMu.Unlock()

	for _, packet := range packets {
		h.broadcastPCM(packet)
	}
}

func audioPacketSize(format capture.Format) int {
	return format.SampleRate * format.BytesPerFrame() * audioPacketMilliseconds / 1000
}

func (h *Hub) snapshotClients() []*clientConnection {
	h.mu.RLock()
	clients := make([]*clientConnection, 0, len(h.clients))
	for _, client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	return clients
}

func (h *Hub) broadcastPCM(data []byte) {
	clients := h.snapshotClients()
	logx.Debugf("webplayer", "WebPlayer: 广播 %d 字节到 %d 个客户端", len(data), len(clients))
	for _, client := range clients {
		clientFmt := h.getClientFormat(client.conn)
		clientData := h.applyFormatConversion(data, h.webFormat, clientFmt)
		h.enqueueAudio(client, clientData)
	}
}

func (h *Hub) enqueueAudio(client *clientConnection, data []byte) {
	select {
	case <-client.done:
		return
	default:
	}

	select {
	case client.audio <- data:
		return
	default:
	}

	// 客户端落后时丢弃最旧音频，保持接近实时而不是继续累积延迟。
	select {
	case <-client.audio:
	default:
	}
	select {
	case client.audio <- data:
	default:
	}
}

func (h *Hub) enqueueControl(client *clientConnection, messageType int, data []byte) {
	msg := outboundMessage{messageType: messageType, data: data}
	select {
	case <-client.done:
		return
	default:
	}

	select {
	case client.control <- msg:
		return
	default:
	}
	select {
	case <-client.control:
	default:
	}
	select {
	case client.control <- msg:
	default:
	}
}

func (h *Hub) clientWriter(client *clientConnection) {
	for {
		var msg outboundMessage
		select {
		case <-client.done:
			return
		case msg = <-client.control:
		default:
			select {
			case <-client.done:
				return
			case msg = <-client.control:
			case data := <-client.audio:
				msg = outboundMessage{messageType: websocket.BinaryMessage, data: data}
			}
		}

		client.conn.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
		if err := client.conn.WriteMessage(msg.messageType, msg.data); err != nil {
			log.Printf("[WebPlayer] WebSocket 发送失败: %v", err)
			h.removeClient(client.conn)
			return
		}
	}
}

// convert32bitTo16bit 将 32-bit float PCM 转换为 16-bit int PCM
// 输入: IEEE 754 float32, 范围 [-1.0, 1.0]
// 输出: signed int16, 范围 [-32768, 32767]
func (h *Hub) convert32bitTo16bit(data []byte) []byte {
	sampleCount := len(data) / 4
	needed := sampleCount * 2
	if cap(h.convBuf) < needed {
		h.convBuf = make([]byte, needed)
	} else {
		h.convBuf = h.convBuf[:needed]
	}

	for i := 0; i < sampleCount; i++ {
		// 读取 float32 (little-endian)
		bits := binary.LittleEndian.Uint32(data[i*4:])
		f := math.Float32frombits(bits)

		// 钳制到 [-1.0, 1.0]
		if f > 1.0 {
			f = 1.0
		} else if f < -1.0 {
			f = -1.0
		}

		// 转换为 int16
		s := int16(f * 32767)

		// 写入 int16 (little-endian)
		h.convBuf[i*2] = byte(s)
		h.convBuf[i*2+1] = byte(s >> 8)
	}

	return h.convBuf
}

// getClientFormat 返回客户端请求的目标格式，未设置时使用 webFormat
func (h *Hub) getClientFormat(conn *websocket.Conn) capture.Format {
	h.clientFmtMu.RLock()
	defer h.clientFmtMu.RUnlock()
	if f, ok := h.clientFormats[conn]; ok {
		return f
	}
	return h.webFormat
}

// presetFromBitrate 将码率请求(kbps)映射为 PCM 格式预设。
// 低码率档位也保留双声道，通过降低采样率/位深控制带宽。
func presetFromBitrate(bitrate int) capture.Format {
	switch {
	case bitrate >= 3072:
		return capture.Format{SampleRate: 96000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 2048:
		return capture.Format{SampleRate: 64000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 1536:
		return capture.Format{SampleRate: 48000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 1024:
		return capture.Format{SampleRate: 32000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 768:
		return capture.Format{SampleRate: 24000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 512:
		return capture.Format{SampleRate: 16000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 384:
		return capture.Format{SampleRate: 12000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 256:
		return capture.Format{SampleRate: 8000, Channels: 2, BitsPerSample: 16}
	case bitrate >= 192:
		return capture.Format{SampleRate: 12000, Channels: 2, BitsPerSample: 8}
	case bitrate >= 128:
		return capture.Format{SampleRate: 8000, Channels: 2, BitsPerSample: 8}
	case bitrate >= 96:
		return capture.Format{SampleRate: 6000, Channels: 2, BitsPerSample: 8}
	default:
		return capture.Format{SampleRate: 4000, Channels: 2, BitsPerSample: 8}
	}
}

// SetClientBitrate 设置指定客户端的码率，并发送 bitrate_changed 确认消息
func (h *Hub) SetClientBitrate(conn *websocket.Conn, bitrate int) {
	target := presetFromBitrate(bitrate)
	h.clientFmtMu.Lock()
	h.clientFormats[conn] = target
	h.clientFmtMu.Unlock()

	// 计算实际码率(kbps)
	actualKbps := target.SampleRate * target.Channels * target.BitsPerSample / 1000

	msg := fmt.Sprintf(
		`{"type":"bitrate_changed","bitrate":%d,"sample_rate":%d,"channels":%d,"bits_per_sample":%d}`,
		actualKbps, target.SampleRate, target.Channels, target.BitsPerSample,
	)
	h.mu.RLock()
	client := h.clients[conn]
	h.mu.RUnlock()
	if client != nil {
		h.enqueueControl(client, websocket.TextMessage, []byte(msg))
	}

	log.Printf("[WebPlayer] 客户端码率已设置: %dkbps → %s", bitrate, target)
}

// applyFormatConversion 将 PCM data 从 src 格式转换为 dst 格式
// 依次执行：采样率转换 → 声道混合 → 位深转换
func (h *Hub) applyFormatConversion(data []byte, src, dst capture.Format) []byte {
	if src == dst || len(data) == 0 {
		return data
	}

	result := data
	cur := src

	// 1. 采样率转换（抽取）
	if cur.SampleRate != dst.SampleRate && cur.SampleRate > 0 && dst.SampleRate > 0 {
		result = convertSampleRate(result, cur.SampleRate, dst.SampleRate, cur.BytesPerFrame())
		cur.SampleRate = dst.SampleRate
	}

	// 2. 声道混合
	if cur.Channels != dst.Channels {
		result = convertChannels(result, cur.Channels, dst.Channels, cur.BitsPerSample/8)
		cur.Channels = dst.Channels
	}

	// 3. 位深转换
	if cur.BitsPerSample != dst.BitsPerSample {
		result = convertBitDepth(result, cur.BitsPerSample, dst.BitsPerSample)
		cur.BitsPerSample = dst.BitsPerSample
	}

	return result
}

// convertSampleRate 通过最近邻重采样转换采样率，支持任意升/降采样比例。
// data 中的帧按 bytesPerFrame 对齐。
func convertSampleRate(data []byte, srcRate, dstRate int, bytesPerFrame int) []byte {
	if srcRate == dstRate || srcRate <= 0 || dstRate <= 0 || bytesPerFrame <= 0 {
		return data
	}
	srcFrames := len(data) / bytesPerFrame
	dstFrames := int(int64(srcFrames) * int64(dstRate) / int64(srcRate))
	if dstFrames == 0 {
		return nil
	}
	result := make([]byte, dstFrames*bytesPerFrame)
	for i := 0; i < dstFrames; i++ {
		srcIndex := int(int64(i) * int64(srcRate) / int64(dstRate))
		if srcIndex >= srcFrames {
			srcIndex = srcFrames - 1
		}
		copy(result[i*bytesPerFrame:], data[srcIndex*bytesPerFrame:])
		_ = result[i*bytesPerFrame+bytesPerFrame-1] // bounds check
	}
	return result
}

// convertChannels 转换声道数（16-bit 立体声↔单声道）
func convertChannels(data []byte, srcCh, dstCh int, bytesPerSample int) []byte {
	if srcCh == dstCh || bytesPerSample <= 0 {
		return data
	}

	// 立体声→单声道：左右声道取平均（16-bit signed）
	if srcCh == 2 && dstCh == 1 && bytesPerSample == 2 {
		frames := len(data) / 4
		if frames == 0 {
			return nil
		}
		result := make([]byte, frames*2)
		for i := 0; i < frames; i++ {
			l := int16(binary.LittleEndian.Uint16(data[i*4:]))
			r := int16(binary.LittleEndian.Uint16(data[i*4+2:]))
			avg := int16((int32(l) + int32(r)) / 2)
			binary.LittleEndian.PutUint16(result[i*2:], uint16(avg))
		}
		return result
	}

	// 单声道→立体声：复制声道（16-bit）
	if srcCh == 1 && dstCh == 2 && bytesPerSample == 2 {
		samples := len(data) / 2
		if samples == 0 {
			return nil
		}
		result := make([]byte, samples*4)
		for i := 0; i < samples; i++ {
			v0 := data[i*2]
			v1 := data[i*2+1]
			result[i*4] = v0
			result[i*4+1] = v1
			result[i*4+2] = v0
			result[i*4+3] = v1
		}
		return result
	}

	return data
}

// convertBitDepth 转换位深（仅支持 16-bit → 8-bit）
func convertBitDepth(data []byte, srcBits, dstBits int) []byte {
	if srcBits == dstBits || len(data) == 0 {
		return data
	}
	if srcBits == 16 && dstBits == 8 {
		samples := len(data) / 2
		result := make([]byte, samples)
		for i := 0; i < samples; i++ {
			s := int16(binary.LittleEndian.Uint16(data[i*2:]))
			// signed 16-bit [-32768,32767] → unsigned 8-bit [0,255]
			result[i] = byte((int32(s) + 32768) / 256)
		}
		return result
	}
	return data
}

// IsStreamingAudio 返回 AudioStream 当前是否正在传输非静音音频
func (h *Hub) IsStreamingAudio() bool {
	last := h.lastNonSilentUnixNano.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < 5*time.Second
}

// Flush 强制刷新剩余的累积数据
func (h *Hub) Flush() {
	h.accMu.Lock()
	data := h.accumBuf
	h.accumBuf = nil
	h.accMu.Unlock()

	if len(data) == 0 {
		return
	}

	logx.Debugf("webplayer", "WebPlayer: Flush %d 字节到 %d 个客户端", len(data), h.ClientCount())
	h.broadcastPCM(data)
}

// removeClient 移除并关闭连接
func (h *Hub) removeClient(conn *websocket.Conn) {
	h.mu.Lock()
	if client, ok := h.clients[conn]; ok {
		delete(h.clients, conn)
		h.mu.Unlock()
		h.clientFmtMu.Lock()
		delete(h.clientFormats, conn)
		h.clientFmtMu.Unlock()
		client.closeOnce.Do(func() {
			close(client.done)
			conn.Close()
		})
		return
	}
	h.mu.Unlock()
}

// ClientCount 返回当前连接的客户端数量
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// HandleWS 处理 WebSocket 连接升级
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebPlayer] WebSocket 升级失败: %v", err)
		return
	}

	// 首先发送音频格式信息（JSON 文本消息）
	// 始终报告 16-bit，因为 Go 端已经做了格式转换
	fmtJSON := fmt.Sprintf(
		`{"type":"format","sample_rate":%d,"channels":%d,"bits_per_sample":16}`,
		h.webFormat.SampleRate, h.webFormat.Channels,
	)
	logx.Debugf("webplayer", "WebPlayer: 发送格式给客户端: %s", fmtJSON)
	conn.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(fmtJSON)); err != nil {
		conn.Close()
		return
	}

	// 注册客户端
	client := &clientConnection{
		conn:    conn,
		audio:   make(chan []byte, clientAudioQueueSize),
		control: make(chan outboundMessage, clientControlQueueSize),
		done:    make(chan struct{}),
	}
	h.mu.Lock()
	h.clients[conn] = client
	count := len(h.clients)
	h.mu.Unlock()
	go h.clientWriter(client)

	log.Printf("[WebPlayer] 🖥️  Web 客户端已连接 (当前共 %d 个)", count)

	// 立即推送当前播放状态，避免客户端等待轮询
	h.BroadcastState(h.queryCombinedState())

	// 保持读循环以检测断开
	defer func() {
		h.removeClient(conn)
		h.mu.RLock()
		remaining := len(h.clients)
		h.mu.RUnlock()
		log.Printf("[WebPlayer] 🔌 Web 客户端已断开 (剩余 %d 个)", remaining)
	}()

	conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		return nil
	})
	// 客户端(OkHttp pingInterval)发的是 Ping 而非 Pong，默认 PongHandler 不会被触发，
	// 5 分钟读超时无法刷新、到点硬关连接（客户端表现为连接中段 EOFException）。
	// 收到 Ping 即为活性证明，刷新读超时并回 Pong（复刻 gorilla 默认 PingHandler 行为）。
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if msgType == websocket.TextMessage {
			if cmd := ParseCommand(msg); cmd != nil {
				logx.Debugf("webplayer", "WebPlayer: 收到命令 action=%s, position=%d, volume=%d, bitrate=%d", cmd.Action, cmd.Position, cmd.Volume, cmd.Bitrate)
				if cmd.Action == "set_bitrate" {
					h.SetClientBitrate(conn, cmd.Bitrate)
				} else if cmd.Action == "get_state" {
					// 客户端主动请求状态，立即查询并广播
					h.BroadcastState(h.queryCombinedState())
				} else if cmd.Action == "play_pause" {
					// play_pause 是切换操作：在异步执行之前主动管理暂停状态。
					// 优先用 SMTC 权威状态判断当前是否播放，避免 5s 音频窗口误判。
					s := querySMTCState()
					if s != nil {
						if s.Playing {
							h.manuallyPaused.Store(true)
						} else {
							h.manuallyPaused.Store(false)
						}
					} else if h.IsStreamingAudio() {
						// 无 SMTC 会话（非媒体音频源）：当前正在播放 → 用户意图暂停
						h.manuallyPaused.Store(true)
					} else if h.manuallyPaused.Load() {
						// 当前是手动暂停 → 用户意图恢复 → 清除标记
						h.manuallyPaused.Store(false)
					}
					// 将最近音频时间置为过去，使 IsStreamingAudio() 立即返回 false
					h.lastNonSilentUnixNano.Store(time.Now().Add(-10 * time.Second).UnixNano())
					ExecuteMediaCommand(cmd)
					// SendInput 是异步的，过早查询会拿到旧状态。
					// 异步延迟查询：不阻塞读循环，给播放器足够时间响应。
					go func() {
						select {
						case <-time.After(800 * time.Millisecond):
							h.BroadcastState(h.queryCombinedState())
						case <-client.done:
							return
						}
					}()
				} else {
					ExecuteMediaCommand(cmd)
					// SendInput 是异步的，过早查询会拿到旧状态。
					// 异步延迟查询：不阻塞读循环，给播放器足够时间响应。
					go func() {
						select {
						case <-time.After(800 * time.Millisecond):
							h.BroadcastState(h.queryCombinedState())
						case <-client.done:
							return
						}
					}()
				}
			}
		}
	}
}

// HandlePage 提供 Web 播放器页面
func (h *Hub) HandlePage(w http.ResponseWriter, r *http.Request) {
	data, err := pageContent.ReadFile("player.html")
	if err != nil {
		http.Error(w, "页面加载失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// HandleIcon provides the AudioStream browser icon.
func (h *Hub) HandleIcon(w http.ResponseWriter, r *http.Request) {
	data, err := pageContent.ReadFile("icon.svg")
	if err != nil {
		http.Error(w, "图标加载失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Write(data)
}

// HandleStats 返回连接统计信息（JSON）
func (h *Hub) HandleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"clients":%d,"format":"%s"}`,
		h.ClientCount(), h.webFormat.String())
}

// StartHTTPServer 启动 HTTP 服务器
func (h *Hub) StartHTTPServer(addr string) error {
	if addr != "" {
		h.addr = addr
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.HandlePage)
	mux.HandleFunc("/icon.svg", h.HandleIcon)
	mux.HandleFunc("/ws", h.HandleWS)
	mux.HandleFunc("/stats", h.HandleStats)

	h.server = &http.Server{
		Addr:    h.addr,
		Handler: mux,
	}

	log.Printf("[WebPlayer] 🌐 Web 播放器已启动: http://%s", h.addr)
	log.Printf("[WebPlayer]    连接后打开浏览器访问 http://localhost%s", h.addr)

	if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop 关闭 HTTP 服务器并取消所有轮询goroutine
func (h *Hub) Stop() error {
	// 取消媒体状态轮询goroutine
	h.cancel()

	for _, client := range h.snapshotClients() {
		h.removeClient(client.conn)
	}

	if h.server != nil {
		return h.server.Close()
	}
	return nil
}
