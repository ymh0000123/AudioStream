//go:build windows

package webplayer

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

//go:embed smtc_embed.dll
var smtcDLLData []byte

var (
	smtcOnce   sync.Once
	smtcLoaded bool
	smtcErr    error

	dllHandle *syscall.DLL
	procInit  *syscall.Proc
	procQuery *syscall.Proc
	procClose *syscall.Proc
)

func loadSmtcDLL() error {
	smtcOnce.Do(func() {
		// Write DLL to a unique temp path based on content hash
		// This avoids file locks from previous crashed processes
		hash := sha256.Sum256(smtcDLLData)
		name := hex.EncodeToString(hash[:8]) + ".dll"
		dllPath := filepath.Join(os.TempDir(), "audiostream", name)

		if err := os.MkdirAll(filepath.Dir(dllPath), 0755); err != nil {
			smtcErr = fmt.Errorf("create temp dir: %w", err)
			return
		}

		// Only write if not already there
		if _, err := os.Stat(dllPath); os.IsNotExist(err) {
			if err := os.WriteFile(dllPath, smtcDLLData, 0755); err != nil {
				smtcErr = fmt.Errorf("write dll: %w", err)
				return
			}
		}

		h, err := syscall.LoadDLL(dllPath)
		if err != nil {
			smtcErr = fmt.Errorf("load dll: %w", err)
			return
		}
		dllHandle = h

		procInit, err = h.FindProc("SmtcInit")
		if err != nil {
			smtcErr = fmt.Errorf("find SmtcInit: %w", err)
			return
		}
		procQuery, err = h.FindProc("SmtcQuery")
		if err != nil {
			smtcErr = fmt.Errorf("find SmtcQuery: %w", err)
			return
		}
		procClose, err = h.FindProc("SmtcClose")
		if err != nil {
			smtcErr = fmt.Errorf("find SmtcClose: %w", err)
			return
		}

		r1, _, _ := procInit.Call()
		if r1 != 0 {
			smtcErr = errors.New("SmtcInit failed")
			return
		}
		smtcLoaded = true
	})
	return smtcErr
}

func querySmtcViaDLL() *smtcState {
	if err := loadSmtcDLL(); err != nil {
		return nil
	}

	buf := make([]byte, 4096)
	r1, _, _ := procQuery.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	n := int32(r1)
	if n <= 0 {
		return nil
	}

	var s smtcState
	if err := json.Unmarshal(buf[:n], &s); err != nil {
		return nil
	}
	if !s.HasSession {
		return nil
	}
	return &s
}

// CloseSmtcDLL 释放 SMTC DLL 资源
func CloseSmtcDLL() {
	if procClose != nil {
		procClose.Call()
	}
	if dllHandle != nil {
		dllHandle.Release()
		dllHandle = nil
	}
}
