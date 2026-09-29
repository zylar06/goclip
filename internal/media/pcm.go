package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// wavHasSignal validates FFmpeg's PCM WAV and checks its decoded samples. Entire
// near-silence (peak <=32/32768, about -60 dBFS) never reaches whisper, avoiding
// silence hallucinations. This is not speech/VAD classification.
func wavHasSignal(path string) (signal bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	var header [12]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return false, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return false, errors.New("expected RIFF/WAVE PCM")
	}
	end := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if end > st.Size() || end < 44 {
		return false, errors.New("truncated or empty WAV")
	}
	offset := int64(12)
	formatOK, dataSeen := false, false
	for offset+8 <= end {
		var chunk [8]byte
		if _, err = io.ReadFull(f, chunk[:]); err != nil {
			return false, err
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		offset += 8
		if size > end-offset {
			return false, errors.New("truncated WAV chunk")
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if size < 16 || size > 1024 {
				return false, errors.New("invalid WAV fmt chunk")
			}
			data := make([]byte, int(size))
			if _, err = io.ReadFull(f, data); err != nil {
				return false, err
			}
			formatOK = binary.LittleEndian.Uint16(data[:2]) == 1 &&
				binary.LittleEndian.Uint16(data[2:4]) == 1 &&
				binary.LittleEndian.Uint32(data[4:8]) == 16000 &&
				binary.LittleEndian.Uint16(data[14:16]) == 16
			if !formatOK {
				return false, errors.New("expected mono 16 kHz signed 16-bit PCM")
			}
		case "data":
			if !formatOK || size == 0 || size%2 != 0 {
				return false, errors.New("invalid or empty WAV sample data")
			}
			dataSeen = true
			var block [32768]byte
			for remaining := size; remaining > 0; {
				n := int(min(remaining, int64(len(block))))
				if _, err = io.ReadFull(f, block[:n]); err != nil {
					return false, err
				}
				for i := 0; i < n; i += 2 {
					v := int16(binary.LittleEndian.Uint16(block[i : i+2]))
					if v > 32 || v < -32 {
						signal = true
					}
				}
				remaining -= int64(n)
			}
		default:
			if _, err = f.Seek(size, io.SeekCurrent); err != nil {
				return false, err
			}
		}
		offset += size
		if size%2 != 0 {
			if _, err = f.Seek(1, io.SeekCurrent); err != nil {
				return false, err
			}
			offset++
		}
	}
	if !formatOK || !dataSeen {
		return false, fmt.Errorf("WAV missing fmt/data chunks")
	}
	return signal, nil
}
