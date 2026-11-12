package chat

import (
	"bufio"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// TestATurnWithARealModel runs one whole turn through the routes with a real
// model behind the model proxy: the production prompt, the production tools
// (the documents are the stand-in shelf, so no index is needed), the real
// stream. It is the test that a scripted model cannot be: that a model given
// this prompt and these tool schemas actually searches, and cites what it
// found.
//
// It runs only when all three are set, and is skipped otherwise:
//
//	LITELLM_URL       the proxy's OpenAI-compatible root, ending in /v1
//	LITELLM_KEY_FILE  a file holding a proxy key
//	LITELLM_MODEL     the proxy's name for the chat model
//
// It costs a few thousand tokens.
func TestATurnWithARealModel(t *testing.T) {
	base, keyFile, name := os.Getenv("LITELLM_URL"), os.Getenv("LITELLM_KEY_FILE"), os.Getenv("LITELLM_MODEL")
	if base == "" || keyFile == "" || name == "" {
		t.Skip("LITELLM_URL, LITELLM_KEY_FILE and LITELLM_MODEL are not all set; this test needs a model proxy")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	client, err := openaicompat.New(openaicompat.Config{
		BaseURL: base, APIKey: strings.TrimSpace(string(key)), Model: name,
		HTTPClient: &http.Client{},
	})
	if err != nil {
		t.Fatal(err)
	}
	real, err := model.WithRetry(client, model.RetryPolicy{Attempts: 2, Backoff: time.Second, MaxBackoff: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(t, real, nil, func(s *Service, sc *scripted) {
		// The assembly's agents and the service share the one store.
		assembly, _, _, _ := production(t, real, real, sc.rows, sc.store, connected{})
		assembly.Models = &Models{
			Catalogue: &offeredModels{names: []string{name}}, Choices: &chosenModels{}, Default: name,
			Open: func(string) (model.Model, error) { return real, nil },
			Own:  ownmodels.NoConnections{},
		}
		s.Assembler = assembly
		s.Titler = ModelTitler{}
		s.Timing.MaxTurn = 2 * time.Minute
		s.Timing.Title = 30 * time.Second
	})

	sent := time.Now()
	response := a.request(ada, http.MethodPost, TurnPath, `{"chat_id":1,"user_query":"According to my notes, what are the phases of mitosis? Answer in one sentence."}`)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
	accepted := time.Since(sent)
	// Read line by line to time the first words, then hand the rest to the
	// reader the other tests use.
	reader := bufio.NewReader(response.Body)
	var head strings.Builder
	var firstText time.Duration
	for firstText == 0 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before any text: %v\n%s", err, head.String())
		}
		head.WriteString(line)
		if strings.Contains(line, `"type": "text-delta"`) {
			firstText = time.Since(sent)
		}
		if strings.Contains(line, `"type": "error"`) {
			t.Fatalf("the turn failed: %s", line)
		}
	}
	rest, done := readFrames(t, reader)
	total := time.Since(sent)
	frames, _ := readFrames(t, strings.NewReader(head.String()))
	frames = append(frames, rest...)
	page := readAll(t, frames)
	if !done || page.failure != nil {
		t.Fatalf("done=%v failure=%v", done, page.failure)
	}

	searched := false
	for _, part := range page.parts {
		if part["type"] == "tool-call" && part["toolName"] == "search_knowledge_base" {
			searched = true
		}
	}
	if !searched {
		t.Errorf("the model answered a question about the user's notes without searching them: %v", kinds(frames))
	}
	saved := a.rows.assistant(1)[0].Content
	if !strings.Contains(saved, "[citation:481]") {
		t.Errorf("the saved answer does not cite the passage it was given:\n%s", saved)
	}
	if !strings.Contains(strings.ToLower(saved), "prophase") {
		t.Errorf("the answer does not come from the passage:\n%s", saved)
	}
	if page.tokenUsage == nil || page.tokenUsage["total_tokens"].(float64) <= 0 {
		t.Errorf("no usage was metered: %v", page.tokenUsage)
	}
	t.Logf("model %s: stream accepted in %s, first words after %s, whole turn %s, %v tokens, title %q",
		name, accepted.Round(time.Millisecond), firstText.Round(time.Millisecond), total.Round(time.Millisecond), page.tokenUsage["total_tokens"], page.title)
}
