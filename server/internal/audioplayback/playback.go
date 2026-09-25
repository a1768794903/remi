package audioplayback

import (
	"bytes"
	"encoding/binary"
	"errors"
)

func ParseRange(header string, size int64) (int64, int64, bool) {
	if size <= 0 || len(header) < 6 || header[:6] != "bytes=" {
		return 0, 0, false
	}
	var start, end int64
	var err error
	spec := header[6:]
	if len(spec) == 0 {
		return 0, 0, false
	}
	for i, c := range spec {
		if c != '-' {
			continue
		}
		if i == 0 {
			var suffix int64
			_, err = parseInt64(spec[1:], &suffix)
			if err != nil || suffix <= 0 {
				return 0, 0, false
			}
			start = size - suffix
			if start < 0 {
				start = 0
			}
			return start, size - 1, true
		}
		_, err = parseInt64(spec[:i], &start)
		if err != nil || start < 0 || start >= size {
			return 0, 0, false
		}
		if i == len(spec)-1 {
			return start, size - 1, true
		}
		_, err = parseInt64(spec[i+1:], &end)
		if err != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
		return start, end, true
	}
	return 0, 0, false
}

func parseInt64(value string, target *int64) (int, error) {
	if value == "" {
		return 0, errors.New("empty integer")
	}
	var result int64
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid integer")
		}
		result = result*10 + int64(c-'0')
		if result < 0 {
			return 0, errors.New("integer overflow")
		}
	}
	*target = result
	return len(value), nil
}

func PCMToWAV(pcm []byte, sampleRate, channels, sampleWidth int) ([]byte, error) {
	if sampleRate <= 0 || channels <= 0 || sampleWidth <= 0 || len(pcm)%(channels*sampleWidth) != 0 {
		return nil, errors.New("invalid PCM parameters")
	}
	dataSize := uint32(len(pcm))
	blockAlign := uint16(channels * sampleWidth)
	byteRate := uint32(sampleRate * int(blockAlign))
	buf := bytes.NewBuffer(make([]byte, 0, 44+len(pcm)))
	write := func(value any) { _ = binary.Write(buf, binary.LittleEndian, value) }
	buf.WriteString("RIFF")
	write(uint32(36) + dataSize)
	buf.WriteString("WAVEfmt ")
	write(uint32(16))
	write(uint16(1))
	write(uint16(channels))
	write(uint32(sampleRate))
	write(byteRate)
	write(blockAlign)
	write(uint16(sampleWidth * 8))
	buf.WriteString("data")
	write(dataSize)
	_, _ = buf.Write(pcm)
	return buf.Bytes(), nil
}
