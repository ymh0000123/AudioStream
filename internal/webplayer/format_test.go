package webplayer

import "testing"

func TestPresetFromBitrateKeepsStereo(t *testing.T) {
	tests := []struct {
		bitrate int
		rate    int
		bits    int
		actual  int
	}{
		{3072, 96000, 16, 3072},
		{2048, 64000, 16, 2048},
		{1536, 48000, 16, 1536},
		{1024, 32000, 16, 1024},
		{768, 24000, 16, 768},
		{512, 16000, 16, 512},
		{384, 12000, 16, 384},
		{256, 8000, 16, 256},
		{192, 12000, 8, 192},
		{128, 8000, 8, 128},
		{96, 6000, 8, 96},
		{64, 4000, 8, 64},
		{1, 4000, 8, 64},
	}

	for _, tt := range tests {
		got := presetFromBitrate(tt.bitrate)
		if got.SampleRate != tt.rate || got.Channels != 2 || got.BitsPerSample != tt.bits {
			t.Fatalf("presetFromBitrate(%d) = %v, want %dHz 2ch %dbit",
				tt.bitrate, got, tt.rate, tt.bits)
		}
		actual := got.SampleRate * got.Channels * got.BitsPerSample / 1000
		if actual != tt.actual {
			t.Fatalf("presetFromBitrate(%d) actual bitrate = %d, want %d",
				tt.bitrate, actual, tt.actual)
		}
	}
}

func TestConvertSampleRateSupportsArbitraryRatios(t *testing.T) {
	src := frames(0, 1, 2, 3, 4, 5)

	down := convertSampleRate(src, 48000, 32000, 2)
	assertFrames(t, down, 0, 1, 3, 4)

	up := convertSampleRate(frames(0, 1, 2, 3), 32000, 48000, 2)
	assertFrames(t, up, 0, 0, 1, 2, 2, 3)
}

func frames(values ...byte) []byte {
	data := make([]byte, len(values)*2)
	for i, v := range values {
		data[i*2] = v
		data[i*2+1] = v
	}
	return data
}

func assertFrames(t *testing.T, got []byte, want ...byte) {
	t.Helper()
	if len(got) != len(want)*2 {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want)*2)
	}
	for i, v := range want {
		if got[i*2] != v || got[i*2+1] != v {
			t.Fatalf("frame %d = [%d %d], want [%d %d]", i, got[i*2], got[i*2+1], v, v)
		}
	}
}
