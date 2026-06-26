// Package transport 提供音频流的网络传输功能
package transport

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
)

// AudioFormat 描述音频流的格式
type AudioFormat struct {
	SampleRate   int `json:"sample_rate"`   // 采样率 (Hz)
	Channels     int `json:"channels"`      // 通道数
	BitsPerSample int `json:"bits_per_sample"` // 位深
}

// Frame 音频数据帧
type Frame struct {
	Data []byte // PCM音频数据
}

// Server 音频传输服务端（发送端）
type Server struct {
	addr       string
	listener   net.Listener
	format     AudioFormat
	onClient   func(clientAddr string)
	onDisconnect func(clientAddr string)
}

// NewServer 创建新的传输服务端
func NewServer(addr string, format AudioFormat) *Server {
	return &Server{
		addr:   addr,
		format: format,
	}
}

// OnClient 设置客户端连接回调
func (s *Server) OnClient(fn func(clientAddr string)) {
	s.onClient = fn
}

// OnDisconnect 设置客户端断开回调
func (s *Server) OnDisconnect(fn func(clientAddr string)) {
	s.onDisconnect = fn
}

// Start 启动服务端监听
func (s *Server) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	return nil
}

// Addr 返回监听地址
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Accept 接受一个客户端连接，返回发送器
func (s *Server) Accept() (*Sender, error) {
	conn, err := s.listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("接受连接失败: %w", err)
	}
	return NewSender(conn, s.format), nil
}

// Close 关闭服务端
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// Sender 音频数据发送器
type Sender struct {
	conn   net.Conn
	format AudioFormat
	enc    *json.Encoder
	dec    *json.Decoder
	buf    []byte
}

// NewSender 创建新的发送器
func NewSender(conn net.Conn, format AudioFormat) *Sender {
	return &Sender{
		conn:   conn,
		format: format,
		enc:    json.NewEncoder(conn),
		dec:    json.NewDecoder(conn),
	}
}

// Handshake 发送音频格式信息并进行握手
func (s *Sender) Handshake() error {
	// 发送音频格式
	if err := s.enc.Encode(s.format); err != nil {
		return fmt.Errorf("发送格式信息失败: %w", err)
	}

	// 等待客户端确认
	var ack string
	if err := s.dec.Decode(&ack); err != nil {
		return fmt.Errorf("接收客户端确认失败: %w", err)
	}
	if ack != "ACK" {
		return fmt.Errorf("客户端确认异常: %s", ack)
	}

	return nil
}

// SendFrame 发送一帧音频数据
// 格式: [4字节数据长度][PCM数据]
func (s *Sender) SendFrame(data []byte) error {
	// 先发送数据长度
	length := uint32(len(data))
	if err := binary.Write(s.conn, binary.BigEndian, length); err != nil {
		return fmt.Errorf("发送数据长度失败: %w", err)
	}

	// 发送音频数据
	if _, err := s.conn.Write(data); err != nil {
		return fmt.Errorf("发送音频数据失败: %w", err)
	}

	return nil
}

// RemoteAddr 返回客户端地址
func (s *Sender) RemoteAddr() string {
	return s.conn.RemoteAddr().String()
}

// Close 关闭发送器
func (s *Sender) Close() error {
	return s.conn.Close()
}
