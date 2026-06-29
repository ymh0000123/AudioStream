package silence

import (
	"math"
)

const (
	thresholdInt16   = 100
	thresholdFloat32 = 0.001
)

func IsSilent(data []byte, bitsPerSample int) bool {
	if len(data) == 0 {
		return true
	}

	switch bitsPerSample {
	case 16:
		return isSilentInt16(data)
	case 32:
		return isSilentFloat32(data)
	default:
		return false
	}
}

func isSilentInt16(data []byte) bool {
	sampleCount := len(data) / 2
	for i := 0; i < sampleCount; i++ {
		sample := int16(data[i*2]) | int16(data[i*2+1])<<8
		if sample < 0 {
			sample = -sample
		}
		if sample > thresholdInt16 {
			return false
		}
	}
	return true
}

func isSilentFloat32(data []byte) bool {
	sampleCount := len(data) / 4
	for i := 0; i < sampleCount; i++ {
		bits := uint32(data[i*4]) | uint32(data[i*4+1])<<8 |
			uint32(data[i*4+2])<<16 | uint32(data[i*4+3])<<24
		f := math.Float32frombits(bits)
		if f < 0 {
			f = -f
		}
		if f > thresholdFloat32 {
			return false
		}
	}
	return true
}
