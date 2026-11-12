package audiofile

import "bytes"

// MP3Seconds is how long MP3 bytes play, counted from their frames in Go.
//
// A line's length is checked on every line of every podcast, and asking
// ffmpeg to decode it costs a process start (about 100 ms here, most of a
// line's overhead); the frames of an MP3 say their own length. The ID3 tag
// in front is skipped, and so is the Xing/Info frame an encoder writes
// first, which holds no sound. Bytes that stop partway count the frames
// that are whole, which is the point: a transfer cut short measures short.
func MP3Seconds(data []byte) float64 {
	data = skipID3(data)
	var samples, rate float64
	first := true
	for len(data) >= 4 {
		header, ok := readFrameHeader(data)
		if !ok {
			// Not a frame where one should be: resynchronise on the next
			// sync word rather than give up on the rest.
			next := bytes.IndexByte(data[1:], 0xFF)
			if next < 0 {
				break
			}
			data = data[1+next:]
			continue
		}
		if header.length > len(data) {
			break
		}
		if !(first && isInfoFrame(data[:header.length])) {
			samples += float64(header.samples)
			rate = float64(header.rate)
		}
		first = false
		data = data[header.length:]
	}
	if rate == 0 {
		return 0
	}
	return samples / rate
}

type frameHeader struct {
	length  int
	samples int
	rate    int
}

var (
	bitratesV1 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	bitratesV2 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	ratesV1    = [4]int{44100, 48000, 32000, 0}
	ratesV2    = [4]int{22050, 24000, 16000, 0}
	ratesV25   = [4]int{11025, 12000, 8000, 0}
)

// readFrameHeader reads a Layer III frame header. Other layers are not
// what a speech service sends and are not counted.
func readFrameHeader(data []byte) (frameHeader, bool) {
	if data[0] != 0xFF || data[1]&0xE0 != 0xE0 {
		return frameHeader{}, false
	}
	version := (data[1] >> 3) & 0x03 // 3 MPEG-1, 2 MPEG-2, 0 MPEG-2.5
	layer := (data[1] >> 1) & 0x03   // 1 is Layer III
	bitrateIndex := data[2] >> 4
	rateIndex := (data[2] >> 2) & 0x03
	padding := int((data[2] >> 1) & 0x01)
	if layer != 1 || version == 1 {
		return frameHeader{}, false
	}
	var bitrate, rate, samples, coefficient int
	switch version {
	case 3:
		bitrate, rate, samples, coefficient = bitratesV1[bitrateIndex], ratesV1[rateIndex], 1152, 144
	case 2:
		bitrate, rate, samples, coefficient = bitratesV2[bitrateIndex], ratesV2[rateIndex], 576, 72
	default:
		bitrate, rate, samples, coefficient = bitratesV2[bitrateIndex], ratesV25[rateIndex], 576, 72
	}
	if bitrate == 0 || rate == 0 {
		return frameHeader{}, false
	}
	return frameHeader{length: coefficient*bitrate*1000/rate + padding, samples: samples, rate: rate}, true
}

// isInfoFrame is the Xing or Info frame an encoder writes first: it carries
// the file's length for players and holds no sound of its own.
func isInfoFrame(frame []byte) bool {
	limit := min(len(frame), 64)
	return bytes.Contains(frame[:limit], []byte("Xing")) || bytes.Contains(frame[:limit], []byte("Info"))
}

// skipID3 drops an ID3v2 tag from the front. Its size is four bytes of
// seven bits each.
func skipID3(data []byte) []byte {
	if len(data) < 10 || !bytes.HasPrefix(data, []byte("ID3")) {
		return data
	}
	size := int(data[6]&0x7F)<<21 | int(data[7]&0x7F)<<14 | int(data[8]&0x7F)<<7 | int(data[9]&0x7F)
	if data[5]&0x10 != 0 {
		size += 10 // a footer
	}
	if 10+size > len(data) {
		return nil
	}
	return data[10+size:]
}
