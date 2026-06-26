# AGENTS.md — AudioStream

Real-time PC audio streaming tool. Captures system audio output (speaker) on one machine and transmits it over the network for playback on another machine or browser.

## Build / Test / Lint

```powershell
# Build (CGO required for WASAPI COM bindings)
$env:CGO_ENABLED='1'
go build -ldflags='-s -w' -o server.exe ./cmd/server
go build -ldflags='-s -w' -o client.exe ./cmd/client

# Windows convenience script
.\build.ps1

# No test files exist yet.
# Lint equivalent:
go vet ./...
```

**Gotcha**: CGO is mandatory on Windows. The `go-ole` and `go-wca` libraries call into the Windows COM API via CGO. Builds **will fail** without `CGO_ENABLED=1` and a working GCC/Clang toolchain (MinGW-w64 recommended).

## Architecture

```
┌─ cmd/server ─────────────────────────────┐
│  capture goroutine  ─┬─ TCP Sender       │
│  (WASAPI / FFmpeg)   ├─ WebSocket Hub    │
│                      └─ channel ── main  │
└──────────────────────────────────────────┘
                          │           │
                     TCP :19730   HTTP :8080
                          │           │
┌─ cmd/client ──┐    ┌── Browser ────┐
│  oto player   │    │ AudioContext  │
│  TCP receiver │    │ WebSocket     │
└───────────────┘    └───────────────┘
```

Data flow: **system audio → Capture interface → [TCP transport | WebSocket] → Player**

## Module Tree

| Path | Responsibility |
|------|---------------|
| `cmd/server/main.go` | Server entry: flags, capture init, background capture goroutine, TCP accept, Web server |
| `cmd/client/main.go` | Client entry: TCP connect, oto player init, receive-and-play loop |
| `internal/capture/` | `Capture` interface + 3 implementations |
| `internal/capture/capture.go` | `Capture` interface (`Format`, `Start`, `Read`, `Stop`, `Close`), `Format` struct, `NewLoopback()` factory |
| `internal/capture/wasapi.go` | Windows WASAPI loopback via go-wca. **Requires `runtime.LockOSThread()`** for COM thread affinity |
| `internal/capture/ffmpeg.go` | FFmpeg subprocess capture. Lists devices, auto-detects Stereo Mix. Build tag: `!stub` |
| `internal/capture/capture_stub.go` | Non-Windows stub returning `ErrUnsupportedPlatform`. Build tag: `!windows` |
| `internal/player/player.go` | Cross-platform oto v3 audio player using `io.Pipe` writer pattern |
| `internal/transport/transport.go` | TCP server/client with JSON handshake + length-prefixed binary frames |
| `internal/webplayer/webplayer.go` | HTTP/WebSocket server. Broadcasts 16-bit PCM to browser clients. Converts 32-bit float → 16-bit int on the fly |
| `internal/webplayer/player.html` | Embedded web UI (embed.FS). Uses AudioContext + scheduled BufferSource for gap-free playback |
| `build.ps1` | Windows build script (sets CGO_ENABLED, builds both binaries) |

## Key Files & Conventions

### Build constraints
- `capture_stub.go`: `//go:build !windows`
- `wasapi.go`: `//go:build windows`
- `ffmpeg.go`: `//go:build !stub` — always included unless explicitly excluded

### Capture interface pattern
```go
type Capture interface {
    Format() Format
    Start() error
    Read(data []byte) (int, error)
    Stop() error
    Close() error
}
```

### Error handling
- Error wrapping with `%w` throughout
- Chinese log prefix convention (e.g. `[AudioStream Server]`, `[WebPlayer]`)
- Server uses `log.Fatalf` for startup errors, `log.Printf` for runtime errors

### COM thread affinity (critical)
Functions in `wasapi.go` **must** call `runtime.LockOSThread()` before `ole.CoInitializeEx()` and `runtime.UnlockOSThread()` before `Close()` returns. All COM operations (`GetDefaultAudioEndpoint`, `Activate`, `GetMixFormat`, `Initialize`, `GetBufferSize`, `GetService`, `Start`, `Stop`, `Read`, `Release`) must execute on the same OS thread. The capture goroutine inherits this locked thread from `newLoopback()`.

### Network protocol (TCP)
1. Server sends JSON `AudioFormat` as first message
2. Client responds with `"ACK"`
3. Server sends length-prefixed PCM frames: `[uint32 BE length][data]`

### WebSocket protocol
1. Server sends JSON format: `{"type":"format","sample_rate":...,"channels":...,"bits_per_sample":16}`
2. Server sends binary 16-bit PCM frames (accumulated to ~50ms per message)

## CLI Flags

### Server
| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:19730` | TCP listen address |
| `-web` | `:8080` | Web player address (empty = disable) |
| `-capture` | `wasapi` | `wasapi` or `ffmpeg` |
| `-device` | `""` | FFmpeg device name (auto-detect if empty) |
| `-list-devices` | `false` | List FFmpeg audio devices and exit |
| `-buf` | `65536` | Audio buffer size (bytes) |

### Client
| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `127.0.0.1:19730` | Server address |

## Git Workflow
- No git repository detected. The project is a standalone working directory.

## Tips for AI Agents

1. **WASAPI captures at system mixer format** — often 96kHz 32-bit float, not the 48kHz 16-bit mentioned in the README. The webplayer hub auto-converts 32→16 bit. The TCP transport sends raw (unconverted) data; the oto player handles both.

2. **No tests exist** — adding tests is high value. The `Capture` interface is mockable directly. The transport's `Sender`/`Client` could benefit from `net.Pipe`-based unit tests.

3. **Server blocks on TCP Accept** — the capture loop runs in a separate goroutine and broadcasts to WebSocket clients independently. TCP client connection is optional (Web-only mode works without it).

4. **Dependencies are version-pinned** in `go.mod`. Update via `go get -u` and commit `go.sum`.

5. **`gorilla/websocket` is an indirect dep** — listed under `// indirect` in `go.mod` but actively used by `internal/webplayer`. This is a quirk of how it was added; don't let `go mod tidy` remove it.

6. **FFmpeg backend** requires FFmpeg on PATH. `server.exe -list-devices` shows available audio inputs. On this system, "立体声混音 (Realtek High Definition Audio)" works for system audio loopback.

7. **Player interface** — `internal/player/player.go` wraps oto v3's `io.Reader`-based API with `io.Pipe`. `Write()` is non-blocking; data is buffered by the pipe.
