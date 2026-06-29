//go:build !windows

package webplayer

func querySmtcViaDLL() *smtcState {
	return nil
}

func closeSmtcDLL() {}
