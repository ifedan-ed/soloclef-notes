package voices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// modelList is a `GET /model/info` answer in the proxy's own shape, cut down
// to what is read and a little of what is not.
const modelList = `{"data": [
  {"model_name": "chat-model", "model_info": {"mode": "chat"}},
  {"model_name": "tts-one", "litellm_params": {"model": "openai/tts-1"},
   "model_info": {"mode": "audio_speech", "voices": ["alloy", "ash", "coral"]}},
  {"model_name": "tts-balanced", "model_info": {"voices": ["alloy", "ash", "coral", "echo"]}},
  {"model_name": "tts-balanced", "model_info": {"voices": ["echo", "coral", "nova"]}},
  {"model_name": "tts-half-listed", "model_info": {"voices": ["alloy"]}},
  {"model_name": "tts-half-listed", "model_info": {"default_voice": "alloy"}},
  {"model_name": "tts-apart", "model_info": {"voices": ["alloy"]}},
  {"model_name": "tts-apart", "model_info": {"voices": ["nova"]}},
  {"model_name": "tts-unlisted", "model_info": {"mode": "audio_speech", "voices": null}}
]}`

// proxyServer is a stand-in for the proxy that records what it was sent.
func proxyServer(t *testing.T, answer http.HandlerFunc) (Proxy, *[]*http.Request, *[][]byte) {
	t.Helper()
	var requests []*http.Request
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests = append(requests, request)
		bodies = append(bodies, body)
		answer(response, request)
	}))
	t.Cleanup(server.Close)
	// A trailing slash on the setting must not double one in the path.
	proxy := NewProxy(Settings{APIBase: server.URL + "/v1/", APIKey: "sk-test"}, server.Client())
	return proxy, &requests, &bodies
}

func TestTheProxyIsAskedWhichVoicesAModelSpeaks(t *testing.T) {
	proxy, requests, _ := proxyServer(t, func(response http.ResponseWriter, _ *http.Request) {
		io.WriteString(response, modelList)
	})
	voices, err := proxy.ModelVoices(context.Background(), "tts-one")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alloy", "ash", "coral"}; !reflect.DeepEqual(voices, want) {
		t.Errorf("tts-one speaks %v, want %v", voices, want)
	}
	sent := (*requests)[0]
	if sent.Method != http.MethodGet || sent.URL.Path != "/v1/model/info" {
		t.Errorf("asked with %s %s", sent.Method, sent.URL.Path)
	}
	if sent.Header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("Authorization %q", sent.Header.Get("Authorization"))
	}
}

// A name the proxy balances between deployments may be answered by any of
// them, so only a voice they all have is offered.
func TestAModelWithSeveralDeploymentsHasTheVoicesTheyShare(t *testing.T) {
	proxy, _, _ := proxyServer(t, func(response http.ResponseWriter, _ *http.Request) {
		io.WriteString(response, modelList)
	})
	voices, err := proxy.ModelVoices(context.Background(), "tts-balanced")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"coral", "echo"}; !reflect.DeepEqual(voices, want) {
		t.Errorf("the deployments share %v, want %v", voices, want)
	}
}

func TestAnAnswerThatShowsAWrongSettingNamesIt(t *testing.T) {
	proxy, _, _ := proxyServer(t, func(response http.ResponseWriter, _ *http.Request) {
		io.WriteString(response, modelList)
	})
	for model, says := range map[string]string{
		"tts-nowhere":     "does not serve",
		"tts-unlisted":    "model_info.voices",
		"tts-half-listed": "model_info.voices",
		"tts-apart":       "no voice in common",
		// A chat model has no voices either; it is the same mistake.
		"chat-model": "model_info.voices",
	} {
		_, err := proxy.ModelVoices(context.Background(), model)
		var wrong *MisconfiguredError
		if !errors.As(err, &wrong) || wrong.Setting != ServiceVariable || !strings.Contains(wrong.Error(), says) {
			t.Errorf("%s: %v; want a MisconfiguredError saying %q", model, err, says)
		}
	}
}

func TestAProxyThatDoesNotAnswerTheQuestionIsUnreachable(t *testing.T) {
	for name, answer := range map[string]http.HandlerFunc{
		"an error": func(response http.ResponseWriter, _ *http.Request) {
			http.Error(response, `{"error": "no healthy upstream"}`, http.StatusBadGateway)
		},
		"a refusal": func(response http.ResponseWriter, _ *http.Request) {
			http.Error(response, `{"error": "invalid key"}`, http.StatusUnauthorized)
		},
		"a page": func(response http.ResponseWriter, _ *http.Request) {
			io.WriteString(response, "<html>welcome</html>")
		},
	} {
		proxy, _, _ := proxyServer(t, answer)
		_, err := proxy.ModelVoices(context.Background(), "tts-one")
		var unreachable *UnreachableError
		if !errors.As(err, &unreachable) {
			t.Errorf("%s: %v; want an UnreachableError", name, err)
		}
	}

	// And one that is not there at all.
	gone := NewProxy(Settings{APIBase: "http://127.0.0.1:1/v1"}, &http.Client{Timeout: time.Second})
	_, err := gone.ModelVoices(context.Background(), "tts-one")
	var unreachable *UnreachableError
	if !errors.As(err, &unreachable) {
		t.Errorf("nothing listening: %v; want an UnreachableError", err)
	}
}

func TestASentenceIsAskedForOnTheProxysSpeechRoute(t *testing.T) {
	proxy, requests, bodies := proxyServer(t, func(response http.ResponseWriter, _ *http.Request) {
		response.Write([]byte("ID3 pretend audio"))
	})
	audio, err := proxy.Speak(context.Background(), "tts-one", "coral", "Hello.")
	if err != nil || string(audio) != "ID3 pretend audio" {
		t.Fatalf("spoke %q, %v", audio, err)
	}
	// Vertex names a voice with an object, and it is sent as one.
	if _, err := proxy.Speak(context.Background(), "studio", map[string]string{"languageCode": "en-UK", "name": "en-UK-Studio-A"}, "Hello."); err != nil {
		t.Fatal(err)
	}

	sent := (*requests)[0]
	if sent.Method != http.MethodPost || sent.URL.Path != "/v1/audio/speech" || sent.Header.Get("Content-Type") != "application/json" {
		t.Errorf("asked with %s %s (%s)", sent.Method, sent.URL.Path, sent.Header.Get("Content-Type"))
	}
	var first, second map[string]any
	json.Unmarshal((*bodies)[0], &first)
	json.Unmarshal((*bodies)[1], &second)
	want := map[string]any{"model": "tts-one", "voice": "coral", "input": "Hello.", "response_format": "mp3"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("sent %v, want %v", first, want)
	}
	if voice, isObject := second["voice"].(map[string]any); !isObject || voice["name"] != "en-UK-Studio-A" {
		t.Errorf("Vertex's voice was sent as %v", second["voice"])
	}
}

func TestAProxyWithNoKeyIsSentNoAuthorization(t *testing.T) {
	var header string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header = request.Header.Get("Authorization")
		response.Write([]byte("ID3"))
	}))
	defer server.Close()
	proxy := NewProxy(Settings{APIBase: server.URL}, server.Client())
	if _, err := proxy.Speak(context.Background(), "m", "v", "t"); err != nil {
		t.Fatal(err)
	}
	if header != "" {
		t.Errorf("Authorization %q was sent for an empty key", header)
	}
}

func TestASpeechServiceThatFailsIsAnError(t *testing.T) {
	for name, answer := range map[string]http.HandlerFunc{
		"a refusal": func(response http.ResponseWriter, _ *http.Request) {
			http.Error(response, `{"error": "voice not found"}`, http.StatusBadRequest)
		},
		"nothing": func(http.ResponseWriter, *http.Request) {},
	} {
		proxy, _, _ := proxyServer(t, answer)
		if audio, err := proxy.Speak(context.Background(), "tts-one", "coral", "Hello."); err == nil {
			t.Errorf("%s: answered %d bytes and no error", name, len(audio))
		}
	}
}

// The variables of the one test here that reaches a real proxy. It asks the
// catalogue question and nothing else: no test in this repository calls the
// speech route, because that is the call that costs money.
//
//	SOLOCLEF_API_TEST_VOICES_PROXY=http://127.0.0.1:4000/v1 \
//	SOLOCLEF_API_TEST_VOICES_PROXY_KEY=sk-… \
//	SOLOCLEF_API_TEST_VOICES_MODEL=openai-tts-1-hd \
//	  go test ./voices/ -run AgainstARealProxy -count=1 -v
const (
	liveProxyVariable = "SOLOCLEF_API_TEST_VOICES_PROXY"
	liveKeyVariable   = "SOLOCLEF_API_TEST_VOICES_PROXY_KEY"
	liveModelVariable = "SOLOCLEF_API_TEST_VOICES_MODEL"
)

func TestTheCatalogueAgainstARealProxy(t *testing.T) {
	base, model := os.Getenv(liveProxyVariable), os.Getenv(liveModelVariable)
	if base == "" || model == "" {
		t.Skipf("%s and %s are not set; this test asks a real proxy for its model list", liveProxyVariable, liveModelVariable)
	}
	settings := Settings{Service: "openai/" + model, APIBase: base, APIKey: os.Getenv(liveKeyVariable)}
	catalogue, err := New(settings, NewProxy(settings, &http.Client{Timeout: 10 * time.Second}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	roster, err := catalogue.Roster(context.Background())
	if err != nil {
		t.Fatalf("the roster of %s: %v", model, err)
	}
	if len(roster.Voices()) == 0 {
		t.Fatalf("%s has no voices", model)
	}
	t.Logf("%s (%s roster) speaks %v", model, roster.Name(), ids(roster.Voices()))
}
