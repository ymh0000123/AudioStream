package webplayer

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"audiostream/internal/logx"
)

type MediaState struct {
	Type       string `json:"type"`
	Playing    bool   `json:"playing"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	PositionMs int64  `json:"position"`
	DurationMs int64  `json:"duration"`
}

var (
	mediaStateMu sync.Mutex
	mediaState   *MediaState
)

// smtcState 是通过 SystemMediaTransportControls (C++/WinRT DLL) 查询到的权威播放状态。
type smtcState struct {
	HasSession bool   `json:"has_session"`
	Playing    bool   `json:"playing"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	PositionMs int64  `json:"position"`
	DurationMs int64  `json:"duration"`
}

// querySMTCState 通过 C++/WinRT DLL 查询 SMTC 播放状态。
func querySMTCState() *smtcState {
	return querySmtcViaDLL()
}

// queryCombinedState combines the authoritative SMTC playback state with audio stream detection
// and manual pause state. The SMTC state (reported by the media app itself) is the source of truth
// for playing/paused — it reflects in-app and physical-key pauses instantly, unlike the 5s audio
// energy window which lags behind actual playback state.
func (h *Hub) queryCombinedState() *MediaState {
	// 手动暂停优先：仅当浏览器端发起暂停且尚无音频恢复时认定暂停。
	// 若 SMTC 已恢复播放，则用户在别处按下了播放，自动清除手动暂停。
	if h.manuallyPaused.Load() {
		if s := querySMTCState(); s != nil && s.Playing {
			logx.Debugf("media", "[MediaState] 手动暂停中检测到 SMTC 已播放, 清除手动暂停")
			h.manuallyPaused.Store(false)
		} else if h.IsStreamingAudio() {
			// 非 SMTC 音频源恢复（如游戏/系统声音），也视为恢复
			logx.Debugf("media", "[MediaState] 手动暂停中检测到音频流, 清除手动暂停")
			h.manuallyPaused.Store(false)
		} else {
			logx.Debugf("media", "[MediaState] 手动暂停, 无音频流")
			return &MediaState{Type: "state", Playing: false}
		}
	}

	// SMTC 是播放/暂停状态的权威来源（无延迟，覆盖应用内与物理键暂停）。
	if s := querySMTCState(); s != nil {
		logx.Debugf("media", "[MediaState] SMTC: playing=%v, title=%q, pos=%d/%d", s.Playing, s.Title, s.PositionMs, s.DurationMs)
		return &MediaState{
			Type:       "state",
			Playing:    s.Playing,
			Title:      s.Title,
			Artist:     s.Artist,
			Album:      s.Album,
			PositionMs: s.PositionMs,
			DurationMs: s.DurationMs,
		}
	}

	// 回退：无 SMTC 会话（游戏/系统声音等非媒体音频源），用音频能量检测。
	if h.IsStreamingAudio() {
		name, _, ok := QueryActiveAudioSessions()
		title := "AudioStream - 系统音频"
		if ok && name != "" {
			title = name
		}
		logx.Debugf("media", "[MediaState] 无 SMTC, 流活跃, 会话: %s", title)
		return &MediaState{Type: "state", Playing: true, Title: title}
	}
	logx.Debugf("media", "[MediaState] 无 SMTC, 无音频流")
	return &MediaState{Type: "state", Playing: false}
}

func GetCachedMediaState() *MediaState {
	mediaStateMu.Lock()
	defer mediaStateMu.Unlock()
	return mediaState
}

func (h *Hub) BroadcastState(state *MediaState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	logx.Debugf("media", "[MediaState] 广播状态: playing=%v, title=%q", state.Playing, state.Title)
	mediaStateMu.Lock()
	mediaState = state
	mediaStateMu.Unlock()
	for _, client := range h.snapshotClients() {
		h.enqueueControl(client, websocket.TextMessage, data)
	}
}

// StartMediaStatePoller starts periodic media state polling.
// 使用 C++/WinRT DLL 后单次查询开销极低（~几 ms），间隔 500ms。
// ctx 用于控制轮询goroutine的退出。
func (h *Hub) StartMediaStatePoller(ctx context.Context) {
	go func() {
		time.Sleep(2 * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.BroadcastState(h.queryCombinedState())
			}
		}
	}()
}
