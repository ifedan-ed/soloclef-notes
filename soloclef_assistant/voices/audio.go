package voices

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// playable is audio a browser can play, with the media type that says what
// it is. The type is read off the bytes rather than trusted from the
// provider's header, because providers behind a proxy mislabel: the same
// `audio/mpeg` header has arrived on MP3 and on bare samples.
//
// Nearly everything answers MP3, which is what is asked for. The exception
// that is known and handled is a provider that answers an MP3 request with
// bare 16-bit mono samples at 24 kHz and no header at all -- Grok Voice
// through OpenRouter does. Those are given a WAV header, which is all a
// browser needs to play them. Anything else -- an error page, a truncated
// answer -- is refused rather than played as noise.
func playable(audio []byte) (Sample, error) {
	if contentType, known := container(audio); known {
		return Sample{Audio: audio, ContentType: contentType}, nil
	}
	if looksLikeText(audio) {
		return Sample{}, errors.New("the answer is text, not audio")
	}
	if len(audio) < minimumBareSamples || len(audio)%2 != 0 {
		return Sample{}, errors.New("the answer is in no known audio container and is not whole 16-bit samples")
	}
	return Sample{Audio: inWAV(audio), ContentType: "audio/wav"}, nil
}

// minimumBareSamples is a tenth of a second at 24 kHz, 16 bits, one channel.
// Less than that is not a spoken sentence.
const minimumBareSamples = 4800

// container names the media type of audio that carries its own header.
func container(audio []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(audio, []byte("ID3")):
		return "audio/mpeg", true
	case len(audio) >= 12 && bytes.HasPrefix(audio, []byte("RIFF")) && bytes.Equal(audio[8:12], []byte("WAVE")):
		return "audio/wav", true
	case bytes.HasPrefix(audio, []byte("OggS")):
		return "audio/ogg", true
	case bytes.HasPrefix(audio, []byte("fLaC")):
		return "audio/flac", true
	case len(audio) >= 2 && audio[0] == 0xFF && audio[1]&0xE0 == 0xE0:
		// An MPEG audio frame. The two layer bits tell MP3 (and its older
		// layers) from AAC in an ADTS stream, which has them at zero.
		if audio[1]&0x06 == 0 {
			return "audio/aac", true
		}
		return "audio/mpeg", true
	}
	return "", false
}

// looksLikeText catches a JSON or HTML error that arrived with a 200.
func looksLikeText(audio []byte) bool {
	trimmed := bytes.TrimLeft(audio, " \t\r\n")
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '<')
}

// inWAV puts the 44-byte header of a 16-bit mono 24 kHz WAV file in front of
// bare samples.
func inWAV(samples []byte) []byte {
	const (
		sampleRate    = 24000
		channels      = 1
		bitsPerSample = 16
		bytesPerFrame = channels * bitsPerSample / 8
	)
	var file bytes.Buffer
	file.Grow(44 + len(samples))
	file.WriteString("RIFF")
	binary.Write(&file, binary.LittleEndian, uint32(36+len(samples)))
	file.WriteString("WAVEfmt ")
	binary.Write(&file, binary.LittleEndian, uint32(16)) // the format chunk's length
	binary.Write(&file, binary.LittleEndian, uint16(1))  // uncompressed PCM
	binary.Write(&file, binary.LittleEndian, uint16(channels))
	binary.Write(&file, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&file, binary.LittleEndian, uint32(sampleRate*bytesPerFrame))
	binary.Write(&file, binary.LittleEndian, uint16(bytesPerFrame))
	binary.Write(&file, binary.LittleEndian, uint16(bitsPerSample))
	file.WriteString("data")
	binary.Write(&file, binary.LittleEndian, uint32(len(samples)))
	file.Write(samples)
	return file.Bytes()
}
