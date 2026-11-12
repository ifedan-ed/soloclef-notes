package voices

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// proxyAt is the address a test's settings name. Nothing is ever sent to it:
// every test here hands the catalogue a stand-in for the proxy.
const proxyAt = "http://proxy.example/v1"

// listing is the proxy for one test: what it says a model speaks, how often
// it was asked, and whether it is down.
type listing struct {
	mutex  sync.Mutex
	voices []string
	err    error
	asked  int
	// models is every model name it was asked about.
	models []string
}

func (l *listing) ModelVoices(_ context.Context, model string) ([]string, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.asked++
	l.models = append(l.models, model)
	return l.voices, l.err
}

func (l *listing) answer(voices []string, err error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.voices, l.err = voices, err
}

func (l *listing) timesAsked() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.asked
}

// neverAsked fails the test if the catalogue asks the proxy anything.
type neverAsked struct{ t *testing.T }

func (n neverAsked) ModelVoices(context.Context, string) ([]string, error) {
	n.t.Error("the proxy was asked about a roster that is written down")
	return nil, errors.New("not to be asked")
}

func ids(voices []Voice) []string {
	out := make([]string, 0, len(voices))
	for _, voice := range voices {
		out = append(out, voice.ID)
	}
	return out
}

func TestAServiceIsReadByExactlyOneRule(t *testing.T) {
	for written, want := range map[string]Service{
		"openai/openrouter-kokoro-82m-tts": {Roster: Kokoro, Model: "openrouter-kokoro-82m-tts"},
		// A Kokoro model is Kokoro whichever API the proxy speaks for it.
		"azure/Kokoro-v1":                      {Roster: Kokoro, Model: "Kokoro-v1"},
		"openai/openai-tts-1-hd":               {Roster: OpenAI, Model: "openai-tts-1-hd"},
		"OpenAI/openrouter-grok-voice-tts-1.0": {Roster: OpenAI, Model: "openrouter-grok-voice-tts-1.0"},
		"azure/tts":                            {Roster: Azure, Model: "tts"},
		// Only the first slash divides: the rest is the model's name.
		"vertex_ai/studio/en": {Roster: Vertex, Model: "studio/en"},
		" openai/tts-1 ":      {Roster: OpenAI, Model: "tts-1"},
	} {
		got, err := readService(written)
		if err != nil {
			t.Errorf("%q was refused: %v", written, err)
			continue
		}
		if got.Roster != want.Roster || got.Model != want.Model {
			t.Errorf("%q read as roster %q, model %q; want %q, %q", written, got.Roster, got.Model, want.Roster, want.Model)
		}
	}
}

func TestAWrongSettingStopsTheCatalogueBeingBuilt(t *testing.T) {
	for name, c := range map[string]struct {
		settings Settings
		setting  string
		says     string
	}{
		"a model on this box":       {Settings{Service: "local/kokoro", APIBase: proxyAt}, ServiceVariable, "on this box"},
		"no model name":             {Settings{Service: "openai", APIBase: proxyAt}, ServiceVariable, "<roster>/<the proxy's model name>"},
		"nothing after the slash":   {Settings{Service: "openai/", APIBase: proxyAt}, ServiceVariable, "<roster>/<the proxy's model name>"},
		"a provider with no roster": {Settings{Service: "elevenlabs/flash", APIBase: proxyAt}, ServiceVariable, `no voices are known for "elevenlabs"`},
		// Without the proxy's address the only way to speak would be to call
		// a provider directly, which this tier never does.
		"no proxy address":          {Settings{Service: "openai/tts-1"}, APIBaseVariable, "is not set"},
		"hosts for no service":      {Settings{PodcastVoices: []string{"coral"}}, PodcastVoicesVariable, "no service"},
		"a host Kokoro lacks":       {Settings{Service: "openai/kokoro", APIBase: proxyAt, PodcastVoices: []string{"af_heart", "coral"}}, PodcastVoicesVariable, "names coral"},
		"a host named twice":        {Settings{Service: "openai/kokoro", APIBase: proxyAt, PodcastVoices: []string{"af_heart", "af_heart"}}, PodcastVoicesVariable, "twice"},
		"an empty host in the list": {Settings{Service: "openai/kokoro", APIBase: proxyAt, PodcastVoices: []string{"af_heart", " "}}, PodcastVoicesVariable, "empty entry"},
	} {
		_, err := New(c.settings, neverAsked{t}, quiet())
		var wrong *MisconfiguredError
		if !errors.As(err, &wrong) {
			t.Errorf("%s: built anyway (error %v)", name, err)
			continue
		}
		if wrong.Setting != c.setting || !strings.Contains(wrong.Error(), c.says) {
			t.Errorf("%s: refused as %q, which should name %s and say %q", name, wrong.Error(), c.setting, c.says)
		}
	}
}

func TestADeploymentWithNoSpeechServiceSaysSo(t *testing.T) {
	catalogue, err := New(Settings{}, neverAsked{t}, quiet())
	if err != nil {
		t.Fatalf("no speech service is a valid deployment: %v", err)
	}
	if _, configured := catalogue.Service(); configured {
		t.Error("a service is reported where none is set")
	}
	if _, err := catalogue.Roster(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("the roster of no service is %v, want ErrNotConfigured", err)
	}
}

func TestKokorosRosterIsWrittenDownAndNeverAsked(t *testing.T) {
	catalogue, err := New(Settings{
		Service: "openai/openrouter-kokoro-82m-tts", APIBase: proxyAt,
		PodcastVoices: []string{"af_heart", "am_adam", "ff_siwis"},
	}, neverAsked{t}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	roster, err := catalogue.Roster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(roster.Voices()); got != 23 {
		t.Errorf("Kokoro has %d voices, want 23", got)
	}

	// A language is matched on its first part, so a British voice is on
	// offer for "en-US" and a Brazilian one for "pt".
	if got, want := ids(roster.ForLanguage("es")), []string{"kokoro:em_alex", "kokoro:ef_dora"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Spanish voices are %v, want %v", got, want)
	}
	if got := len(roster.ForLanguage("EN-au")); got != 10 {
		t.Errorf("%d voices speak English, want 10", got)
	}
	if got, want := ids(roster.ForLanguage("pt")), []string{"kokoro:pm_alex", "kokoro:pf_dora"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Portuguese voices are %v, want %v", got, want)
	}
	if roster.Supports("de") || len(roster.ForLanguage("de")) != 0 {
		t.Error("Kokoro is said to speak German, and has no German voice")
	}

	// Every voice has a language of its own, so the languages are exactly
	// those and no other may be typed in.
	offering := roster.Languages()
	want := []string{"en-GB", "en-US", "es", "fr", "hi", "it", "ja", "pt-BR", "zh"}
	if !reflect.DeepEqual(offering.Languages, want) || offering.AllowsCustom {
		t.Errorf("the languages on offer are %v (custom %v), want %v and no custom", offering.Languages, offering.AllowsCustom, want)
	}

	// The configured hosts, in the order configured, that speak the language.
	if got, want := ids(roster.Hosts("en")), []string{"kokoro:af_heart", "kokoro:am_adam"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the English hosts are %v, want %v", got, want)
	}
	if got, want := ids(roster.Hosts("fr")), []string{"kokoro:ff_siwis"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the French hosts are %v, want %v", got, want)
	}

	if _, found := roster.Voice("openai:coral"); found {
		t.Error("an OpenAI voice was found in Kokoro's roster")
	}
	if voice, found := roster.Voice("kokoro:bf_emma"); !found || voice.Native != "bf_emma" || voice.Gender != Female {
		t.Errorf("kokoro:bf_emma is %+v (found %v)", voice, found)
	}
}

func TestVertexNamesAVoiceToTheServiceWithAnObject(t *testing.T) {
	catalogue, err := New(Settings{Service: "vertex_ai/studio", APIBase: proxyAt}, neverAsked{t}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	roster, _ := catalogue.Roster(context.Background())
	voice, found := roster.Voice("vertex_ai:en-GB-Studio-A")
	want := map[string]string{"languageCode": "en-UK", "name": "en-UK-Studio-A"}
	if !found || !reflect.DeepEqual(voice.Native, want) || voice.Language != "en-GB" {
		t.Errorf("the British studio voice is %+v (found %v)", voice, found)
	}
}

// openAICatalogue is a catalogue over an OpenAI-style model, with a clock a
// test moves by hand.
func openAICatalogue(t *testing.T, proxy Lister, hosts ...string) (*Catalogue, *time.Time) {
	t.Helper()
	catalogue, err := New(Settings{Service: "openai/openai-tts-1-hd", APIBase: proxyAt, PodcastVoices: hosts}, proxy, quiet())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	catalogue.now = func() time.Time { return clock }
	return catalogue, &clock
}

func TestAnOpenAIModelHasTheVoicesTheProxySaysItHas(t *testing.T) {
	// In the proxy's order, with a voice this package has never heard of.
	proxy := &listing{voices: []string{"shimmer", "juniper", "coral", "alloy"}}
	catalogue, _ := openAICatalogue(t, proxy, "coral")
	roster, err := catalogue.Roster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// In the catalogue's order, and without the stranger.
	if got, want := ids(roster.Voices()), []string{"openai:alloy", "openai:coral", "openai:shimmer"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the roster is %v, want %v", got, want)
	}
	if !reflect.DeepEqual(proxy.models, []string{"openai-tts-1-hd"}) {
		t.Errorf("the proxy was asked about %v; it knows the model without the setting's first segment", proxy.models)
	}
	// Such voices speak anything: every language, the common ones offered
	// up front, and any other may be typed.
	if !roster.Supports("sw") || !roster.Supports("xx") {
		t.Error("a voice that speaks any language was not offered for one")
	}
	offering := roster.Languages()
	if !offering.AllowsCustom || len(offering.Languages) != len(commonLanguages) || offering.Languages[0] != "ar" {
		t.Errorf("the offering is %+v", offering)
	}
}

func TestTheProxysAnswerIsKeptForItsLifetimeAndThenAskedAgain(t *testing.T) {
	proxy := &listing{voices: []string{"alloy", "coral"}}
	catalogue, clock := openAICatalogue(t, proxy)
	for range 5 {
		if _, err := catalogue.Roster(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if proxy.timesAsked() != 1 {
		t.Fatalf("the proxy was asked %d times for five rosters, want once", proxy.timesAsked())
	}

	// The model is re-pointed in the proxy. Nothing restarts.
	proxy.answer([]string{"alloy", "coral", "marin"}, nil)
	*clock = clock.Add(rosterLifetime - time.Second)
	if roster, _ := catalogue.Roster(context.Background()); len(roster.Voices()) != 2 {
		t.Error("the roster changed before its lifetime was up")
	}
	*clock = clock.Add(2 * time.Second)
	roster, err := catalogue.Roster(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, found := roster.Voice("openai:marin"); !found || proxy.timesAsked() != 2 {
		t.Errorf("after its lifetime the roster is %v, asked %d times", ids(roster.Voices()), proxy.timesAsked())
	}
}

func TestRequestsThatArriveTogetherAskTheProxyOnce(t *testing.T) {
	proxy := &listing{voices: []string{"alloy"}}
	catalogue, _ := openAICatalogue(t, proxy)
	var together sync.WaitGroup
	for range 40 {
		together.Add(1)
		go func() {
			defer together.Done()
			if _, err := catalogue.Roster(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	together.Wait()
	if proxy.timesAsked() != 1 {
		t.Errorf("forty requests asked the proxy %d times", proxy.timesAsked())
	}
}

// The Python catalogue guessed a roster from the model's name here. This one
// says it does not know, and asks again shortly.
func TestAProxyThatCannotBeAskedIsAnErrorAndNotAGuess(t *testing.T) {
	proxy := &listing{err: errors.New("connection refused")}
	catalogue, clock := openAICatalogue(t, proxy)
	for range 3 {
		_, err := catalogue.Roster(context.Background())
		var unreachable *UnreachableError
		if !errors.As(err, &unreachable) {
			t.Fatalf("the roster of an unreachable proxy is %v, want an UnreachableError", err)
		}
	}
	if proxy.timesAsked() != 1 {
		t.Errorf("a proxy that is down was asked %d times in a row; the failure is remembered for %s", proxy.timesAsked(), retryAfter)
	}

	// It comes back.
	proxy.answer([]string{"alloy"}, nil)
	*clock = clock.Add(retryAfter + time.Second)
	if _, err := catalogue.Roster(context.Background()); err != nil {
		t.Errorf("the proxy is back and the roster is still %v", err)
	}
}

func TestAnOutageAfterAnAnswerKeepsThatAnswer(t *testing.T) {
	proxy := &listing{voices: []string{"alloy", "coral"}}
	catalogue, clock := openAICatalogue(t, proxy)
	if _, err := catalogue.Roster(context.Background()); err != nil {
		t.Fatal(err)
	}
	proxy.answer(nil, &UnreachableError{Cause: errors.New("502 from the proxy")})
	*clock = clock.Add(rosterLifetime + time.Second)
	for range 3 {
		roster, err := catalogue.Roster(context.Background())
		if err != nil || len(roster.Voices()) != 2 {
			t.Fatalf("during an outage the roster is %v, %v; want the proxy's last answer", roster, err)
		}
	}
	if proxy.timesAsked() != 2 {
		t.Errorf("asked %d times; the outage is retried once per %s, not per request", proxy.timesAsked(), retryAfter)
	}
}

// An answer that shows the setting is wrong is not an outage: the earlier
// roster is known to be wrong now, and is not served.
func TestAnAnswerThatShowsAWrongSettingReplacesTheOldRoster(t *testing.T) {
	proxy := &listing{voices: []string{"alloy", "coral"}}
	catalogue, clock := openAICatalogue(t, proxy, "coral")
	if _, err := catalogue.Roster(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The model is re-pointed at one that has no coral.
	proxy.answer([]string{"ara", "rex"}, nil)
	*clock = clock.Add(rosterLifetime + time.Second)
	_, err := catalogue.Roster(context.Background())
	var wrong *MisconfiguredError
	if !errors.As(err, &wrong) || wrong.Setting != PodcastVoicesVariable || !strings.Contains(wrong.Error(), "coral") {
		t.Errorf("a host the model no longer has is %v; want an error naming %s and coral", err, PodcastVoicesVariable)
	}
}

func TestAModelWhoseVoicesAreAllUnknownIsAWrongSetting(t *testing.T) {
	proxy := &listing{voices: []string{"Zephyr", "Puck"}}
	catalogue, _ := openAICatalogue(t, proxy)
	_, err := catalogue.Roster(context.Background())
	var wrong *MisconfiguredError
	if !errors.As(err, &wrong) || wrong.Setting != ServiceVariable || !strings.Contains(wrong.Error(), "Zephyr") {
		t.Errorf("a model with only unknown voices is %v", err)
	}
}

// What the data itself must hold for the rest to be true.
func TestEveryVoiceIsNamedOnceAndUnderItsRoster(t *testing.T) {
	for name, written := range map[RosterName][]Voice{
		Kokoro: kokoroVoices, OpenAI: openAIVoices, Azure: azureVoices, Vertex: vertexVoices,
	} {
		seen := map[string]bool{}
		for _, voice := range written {
			if !strings.HasPrefix(voice.ID, string(name)+":") {
				t.Errorf("%s is in the %s roster", voice.ID, name)
			}
			if seen[voice.ID] {
				t.Errorf("%s is written twice", voice.ID)
			}
			seen[voice.ID] = true
			if voice.DisplayName == "" || voice.Native == nil || voice.Language == "" {
				t.Errorf("%s is incomplete: %+v", voice.ID, voice)
			}
			if voice.Gender != Male && voice.Gender != Female && voice.Gender != Neutral {
				t.Errorf("%s has gender %q", voice.ID, voice.Gender)
			}
		}
	}
}

func TestEveryVoiceHasASentenceInItsOwnLanguage(t *testing.T) {
	for _, written := range [][]Voice{kokoroVoices, openAIVoices, azureVoices, vertexVoices} {
		for _, voice := range written {
			if _, found := sampleTexts[primary(sampleLanguage(voice))]; !found {
				t.Errorf("%s speaks %s and there is no sample sentence in it", voice.ID, voice.Language)
			}
		}
	}
}
