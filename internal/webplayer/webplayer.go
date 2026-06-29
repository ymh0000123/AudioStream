// Package webplayer 提供 Web 浏览器端的音频播放功能
// 通过 WebSocket 将 PCM 音频推送到浏览器，使用 AudioContext 播放
package webplayer

import (
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

//go:embed player.html
var pageContent embed.FS

// Hub 管理 WebSocket 连接并将音频数据广播给所有连接的浏览器
type Hub struct {
	clients   map[*websocket.Conn]bool
	mu        sync.RWMutex
	writeMu   sync.Mutex // 序列化所有 WebSocket 写操作，防止并发写导致消息损坏
	format    capture.Format
	webFormat capture.Format // 发送给浏览器的格式（始终为 16-bit）
	upgrader  websocket.Upgrader
	server    *http.Server
	addr      string
	accumBuf  []byte // 累积缓冲区，减少发送频率
	accMu     sync.Mutex
	convBuf   []byte // 格式转换缓冲区

	lastNonSilentAt time.Time   // 最近一次非静音数据经过 Broadcast 的时间
	manuallyPaused  atomic.Bool // 客户端通过浏览器手动暂停，覆盖音频检测
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

	return &Hub{
		clients:   make(map[*websocket.Conn]bool),
		format:    format,
		webFormat: webFmt,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // 允许跨域连接
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 65536,
		},
		addr: ":8080",
	}
}

// Broadcast 向所有连接的 WebSocket 客户端广播音频数据
func (h *Hub) Broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.clients) == 0 {
		return
	}

	// 静音检测：跳过静音数据的转换和广播
	if silence.IsSilent(data, h.format.BitsPerSample) {
		return
	}

	h.lastNonSilentAt = time.Now()

	// 如果源数据是 32-bit float，转换为 16-bit int
	if h.format.BitsPerSample == 32 {
		logx.Debugf("webplayer", "WebPlayer: 32-bit → 16-bit 转换 %d 字节", len(data))
		data = h.convert32bitTo16bit(data)
	}

	// 累积到缓冲区，每 ~50ms 发送一次，减少浏览器端播放间隙
	h.accMu.Lock()
	h.accumBuf = append(h.accumBuf, data...)
	accLen := len(h.accumBuf)
	h.accMu.Unlock()

	// 计算目标缓冲区大小：50ms 的 16-bit 音频数据
	targetSize := h.webFormat.SampleRate * h.webFormat.BytesPerFrame() / 20 // 50ms
	logx.Debugf("webplayer", "WebPlayer: 累积缓冲 %d/%d 字节 (目标 %d)", accLen, cap(h.accumBuf), targetSize)
	if accLen < targetSize {
		return
	}

	// 取出累积数据
	h.accMu.Lock()
	sendData := make([]byte, len(h.accumBuf))
	copy(sendData, h.accumBuf)
	h.accumBuf = h.accumBuf[:0]
	h.accMu.Unlock()

	// 广播给所有客户端（序列化写入，防止并发写损坏消息）
	h.writeMu.Lock()
	logx.Debugf("webplayer", "WebPlayer: 广播 %d 字节到 %d 个客户端", len(sendData), len(h.clients))
	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.BinaryMessage, sendData); err != nil {
			log.Printf("[WebPlayer] ⚠️  WebSocket 发送失败: %v", err)
			go h.removeClient(conn)
		}
	}
	h.writeMu.Unlock()
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

// IsStreamingAudio 返回 AudioStream 当前是否正在传输非静音音频
func (h *Hub) IsStreamingAudio() bool {
	return time.Since(h.lastNonSilentAt) < 5*time.Second
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

	h.mu.RLock()
	defer h.mu.RUnlock()
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			go h.removeClient(conn)
		}
	}
}

// removeClient 移除并关闭连接
func (h *Hub) removeClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[conn]; ok {
		delete(h.clients, conn)
		conn.Close()
	}
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
	h.writeMu.Lock()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(fmtJSON)); err != nil {
		h.writeMu.Unlock()
		conn.Close()
		return
	}
	h.writeMu.Unlock()

	// 注册客户端
	h.mu.Lock()
	h.clients[conn] = true
	count := len(h.clients)
	h.mu.Unlock()

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

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if msgType == websocket.TextMessage {
			if cmd := ParseCommand(msg); cmd != nil {
				logx.Debugf("webplayer", "WebPlayer: 收到命令 action=%s, position=%d, volume=%d", cmd.Action, cmd.Position, cmd.Volume)
				if cmd.Action == "get_state" {
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
					// 将 lastNonSilentAt 置为过去，使 IsStreamingAudio() 立即返回 false
					h.lastNonSilentAt = time.Now().Add(-10 * time.Second)
					ExecuteMediaCommand(cmd)
					// SendInput 是异步的，过早查询会拿到旧状态。
					// 异步延迟查询：不阻塞读循环，给播放器足够时间响应。
					go func() {
						time.Sleep(800 * time.Millisecond)
						h.BroadcastState(h.queryCombinedState())
					}()
				} else {
					ExecuteMediaCommand(cmd)
					// SendInput 是异步的，过早查询会拿到旧状态。
					// 异步延迟查询：不阻塞读循环，给播放器足够时间响应。
					go func() {
						time.Sleep(800 * time.Millisecond)
						h.BroadcastState(h.queryCombinedState())
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

// Stop 关闭 HTTP 服务器
func (h *Hub) Stop() error {
	h.mu.Lock()
	for conn := range h.clients {
		conn.Close()
		delete(h.clients, conn)
	}
	h.mu.Unlock()

	if h.server != nil {
		return h.server.Close()
	}
	return nil
}
