//go:build !windows

package capture

func newLoopback() (Capture, error) {
	return nil, ErrUnsupportedPlatform
}
