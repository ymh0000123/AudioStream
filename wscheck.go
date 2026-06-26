package main

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	u := url.URL{Scheme: "ws", Host: "localhost:8081", Path: "/ws"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Println("❌ 连接失败:", err)
		os.Exit(1)
	}
	defer c.Close()

	// 读取格式信息
	_, msg, err := c.ReadMessage()
	if err != nil {
		fmt.Println("❌ 读取格式信息失败:", err)
		os.Exit(1)
	}
	fmt.Println("✅ 格式信息:", string(msg))

	// 读取 5 秒的音频数据
	deadline := time.After(5 * time.Second)
	totalBytes := 0
	packets := 0
	for {
		select {
		case <-deadline:
			fmt.Printf("\n📊 5秒内收到 %d 包 / %d 字节\n", packets, totalBytes)
			if totalBytes > 0 {
				fmt.Println("✅ WebSocket 音频流正常工作!")
			} else {
				fmt.Println("❌ 没有收到任何音频数据")
			}
			return
		default:
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, data, err := c.ReadMessage()
			if err != nil {
				fmt.Println("\n读取错误:", err)
				return
			}
			totalBytes += len(data)
			packets++
			if packets <= 3 {
				fmt.Printf("  包%d: %d 字节\n", packets, len(data))
			} else if packets == 4 {
				fmt.Printf("  ... (更多数据)\n")
			}
		}
	}
}
