package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

func (t *Tools) Probe(ctx context.Context, path string) (Info, error) {
	path, err := t.localFile(ctx, path)
	if err != nil {
		return Info{}, err
	}
	data, err := run(ctx, command{exe: t.cfg.FFprobe, timeout: 30 * time.Second, stdoutLimit: 1 << 20,
		args: []string{"-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries",
			"format=duration:stream=codec_type,width,height,duration:stream_tags=rotate:stream_side_data=rotation:stream_disposition=attached_pic",
			"-of", "json", path}})
	if err != nil {
		return Info{}, err
	}
	var raw struct {
		Streams []struct {
			CodecType   string `json:"codec_type"`
			Width       int
			Height      int
			Duration    string
			Tags        struct{ Rotate string }
			SideData    []struct{ Rotation float64 } `json:"side_data_list"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			}
		}
		Format struct{ Duration string }
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return Info{}, fmt.Errorf("ffprobe JSON: %w", err)
	}
	var info Info
	// N/A is common on streams; a finite container duration is preferred.
	info.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, stream := range raw.Streams {
		if stream.CodecType == "audio" {
			info.HasAudio = true
		}
		if stream.CodecType != "video" || info.Width != 0 {
			continue
		}
		if stream.Disposition.AttachedPic != 0 {
			return Info{}, errors.New("media: attached-picture primary video is unsupported")
		}
		info.Width, info.Height = stream.Width, stream.Height
		rotation, _ := strconv.ParseFloat(stream.Tags.Rotate, 64)
		for _, side := range stream.SideData {
			rotation = side.Rotation
		}
		if math.Abs(math.Mod(rotation, 180)) == 90 {
			info.Width, info.Height = info.Height, info.Width
		}
		if !finite(info.Duration) || info.Duration <= 0 {
			info.Duration, _ = strconv.ParseFloat(stream.Duration, 64)
		}
	}
	if info.Width < 2 || info.Height < 2 || info.Width > 16384 || info.Height > 16384 ||
		!finite(info.Duration) || info.Duration <= 0 {
		return Info{}, errors.New("media: ffprobe returned no valid finite-duration video")
	}
	if info.Duration > t.cfg.MaxDuration {
		return Info{}, fmt.Errorf("media duration %.3fs exceeds limit %.3fs", info.Duration, t.cfg.MaxDuration)
	}
	return info, nil
}
