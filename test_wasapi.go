package main

import (
	"fmt"
	"time"

	"audiostream/internal/capture"
)

func main() {
	fmt.Println("Testing WASAPI capture...")
	cap, err := capture.NewLoopback()
	if err != nil {
		fmt.Println("FAILED:", err)
		return
	}
	fmt.Println("Format:", cap.Format())

	if err := cap.Start(); err != nil {
		fmt.Println("Start FAILED:", err)
		return
	}
	fmt.Println("Started!")

	buf := make([]byte, 65536)
	deadline := time.After(3 * time.Second)
	total := 0

	for {
		select {
		case <-deadline:
			fmt.Printf("3秒内读到 %d 字节\n", total)
			cap.Stop()
			cap.Close()
			return
		default:
			n, err := cap.Read(buf)
			if err != nil {
				fmt.Println("Read error:", err)
				continue
			}
			if n == 0 {
				continue
			}
			total += n
			if total <= 100000 {
				fmt.Printf("  读取 %d 字节 (累计 %d)\n", n, total)
			}
		}
	}
}
