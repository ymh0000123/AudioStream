package webplayer

import "encoding/json"

type MediaCommand struct {
	Type     string `json:"type"`
	Action   string `json:"action"`
	Position int64  `json:"position"`
	Volume   int    `json:"volume"`
	Bitrate  int    `json:"bitrate"`
}

func ParseCommand(data []byte) *MediaCommand {
	var cmd MediaCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil
	}
	if cmd.Type != "command" {
		return nil
	}
	switch cmd.Action {
	case "play_pause", "previous", "next", "seek_to", "set_volume", "get_state", "set_bitrate":
		return &cmd
	default:
		return nil
	}
}
