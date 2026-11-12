// Package proxystub is a stand-in for the model proxy, for tests: it speaks
// the two OpenAI shapes the worker sends, a streamed chat completion and a
// speech request, and answers each from a function the test supplies. It
// records what it was asked, so a test can check the request as well as
// the outcome.
//
// It is a package of its own, not a _test file, because the llm, speech and
// podcast tests all need the same proxy, and a second copy of the stream
// format would be a second spelling to keep in step.
package proxystub

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
)

// ChatRequest is the part of a chat completion request a test reads.
type ChatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream   bool           `json:"stream"`
	Thinking map[string]any `json:"thinking"`
}

// System is the request's system message, or "".
func (r ChatRequest) System() string {
	for _, message := range r.Messages {
		if message.Role == "system" {
			return message.Content
		}
	}
	return ""
}

// Last is the request's last message.
func (r ChatRequest) Last() string {
	if len(r.Messages) == 0 {
		return ""
	}
	return r.Messages[len(r.Messages)-1].Content
}

// SpeechRequest is a speech request as sent.
type SpeechRequest struct {
	Model          string `json:"model"`
	Voice          any    `json:"voice"`
	Input          string `json:"input"`
	ResponseFormat string `json:"response_format"`
	Instructions   string `json:"instructions"`
}

// Server is the stub. Chat and Speech may be swapped by a test before a
// request is made; each is called under the server's lock, one request at
// a time, so a test's function needs no locking of its own.
type Server struct {
	*httptest.Server

	mu sync.Mutex
	// Chat answers a chat completion with its text. An error status makes
	// it answer that status instead.
	Chat func(ChatRequest) (text string, status int)
	// Speech answers a speech request with its audio bytes, or a status.
	Speech func(SpeechRequest) (audio []byte, status int)

	chats    []ChatRequest
	speeches []SpeechRequest
}

// Start serves the stub on a loopback port. The caller closes it.
func Start() *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	mux.HandleFunc("POST /v1/audio/speech", s.speech)
	s.Server = httptest.NewServer(mux)
	return s
}

// BaseURL is the stub's OpenAI-style root, as the worker is configured with
// the proxy's.
func (s *Server) BaseURL() string { return s.URL + "/v1" }

// Chats is every chat request so far, in order.
func (s *Server) Chats() []ChatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChatRequest(nil), s.chats...)
}

// Speeches is every speech request so far, in order.
func (s *Server) Speeches() []SpeechRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SpeechRequest(nil), s.speeches...)
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var request ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.chats = append(s.chats, request)
	text, status := "", http.StatusOK
	if s.Chat != nil {
		text, status = s.Chat(request)
	}
	s.mu.Unlock()
	if status != http.StatusOK {
		http.Error(w, `{"error":{"message":"stub refused"}}`, status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	// The answer in two fragments, as a real stream splits it, then the
	// finish and the usage, then the end marker.
	half := len(text) / 2
	for _, fragment := range []string{text[:half], text[half:]} {
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": fragment}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (s *Server) speech(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var request SpeechRequest
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.speeches = append(s.speeches, request)
	audio, status := Tone(0.4), http.StatusOK
	if s.Speech != nil {
		audio, status = s.Speech(request)
	}
	s.mu.Unlock()
	if status != http.StatusOK {
		http.Error(w, `{"error":{"message":"stub refused"}}`, status)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	_, _ = w.Write(audio)
}

// Tone is seconds of a 440 Hz tone as a 16-bit mono 24 kHz WAV file: real
// audio that ffmpeg decodes, so a test exercises the same transcoding and
// measuring a provider's answer gets.
func Tone(seconds float64) []byte {
	return wav(PCM(seconds))
}

// PCM is the same tone as bare samples with no header, the way one provider
// answers an MP3 request.
func PCM(seconds float64) []byte {
	const rate = 24000
	count := int(seconds * rate)
	samples := make([]byte, 2*count)
	for i := 0; i < count; i++ {
		value := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/rate))
		binary.LittleEndian.PutUint16(samples[2*i:], uint16(value))
	}
	return samples
}

func wav(samples []byte) []byte {
	const rate, bits, channels = 24000, 16, 1
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(samples)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], channels)
	binary.LittleEndian.PutUint32(header[24:], rate)
	binary.LittleEndian.PutUint32(header[28:], rate*channels*bits/8)
	binary.LittleEndian.PutUint16(header[32:], channels*bits/8)
	binary.LittleEndian.PutUint16(header[34:], bits)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(samples)))
	return append(header, samples...)
}
