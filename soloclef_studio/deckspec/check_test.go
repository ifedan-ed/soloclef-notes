package deckspec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validDeck() Deck {
	return Deck{
		Title: "Photosynthesis", Theme: "clinical",
		Sources: []Source{{Title: "Campbell Biology", Detail: "2020"}},
		Slides: []Slide{
			{Kind: KindTitle, Title: "Photosynthesis", Subtitle: "How plants make food"},
			{Kind: KindBullets, Title: "Inputs and outputs", Bullets: []string{"• **Inputs:** CO2 and water", "Outputs:   glucose and oxygen"}, Cites: []int{1}},
			{Kind: KindDiagram, Title: "The light reactions", Style: DiagramFlow, Steps: []Step{{Label: "Light"}, {Label: "Water split"}, {Label: "ATP"}}},
		},
	}
}

func TestAValidDeckIsPutInOneSpelling(t *testing.T) {
	deck, err := Check(validDeck(), Options{MinSlides: 3, MaxSlides: 30})
	if err != nil {
		t.Fatal(err)
	}
	// The bullet glyph the model wrote is taken off; spaces are folded.
	if got := deck.Slides[1].Bullets; got[0] != "**Inputs:** CO2 and water" || got[1] != "Outputs: glucose and oxygen" {
		t.Errorf("bullets %q", got)
	}
}

func TestEveryProblemIsSaidAtOnce(t *testing.T) {
	deck := validDeck()
	deck.Theme = "neon"
	deck.Slides[1].Bullets = []string{"only one"}
	deck.Slides[1].Cites = []int{1, 1, 4}
	deck.Slides[1].Quote = "stray"
	deck.Slides[2].Style = "spiral"
	deck.Slides = append(deck.Slides, Slide{Kind: "poster", Title: "x"}, Slide{Kind: KindChart, Title: "Bars", Bars: []Bar{{Label: "a", Value: -1}, {Label: "b", Value: 0}}})
	_, err := Check(deck, Options{MinSlides: 3, MaxSlides: 4})
	problems, ok := err.(Problems)
	if !ok {
		t.Fatalf("err %v", err)
	}
	all := strings.Join(problems, "\n")
	for _, want := range []string{
		`theme "neon" is not a theme`,
		"the deck must have 3 to 4 slides; it has 5",
		"slide 2 (bullets) needs 2 to 6 bullets; it has 1",
		"slide 2 (bullets) cites source 1 twice",
		"slide 2 (bullets) cites source 4, and the deck lists 1 sources",
		"slide 2 (bullets) has quote, which a bullets slide does not have",
		`slide 3 (diagram) style must be "flow" or "cycle"; it is "spiral"`,
		`slide 4: kind "poster" is not a slide kind`,
		"slide 5 (chart) bar 1's value must be a number of zero or more",
		"slide 5 (chart) has no bar above zero",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in:\n%s", want, all)
		}
	}
}

func TestLimitsAreToldInWords(t *testing.T) {
	deck := validDeck()
	deck.Slides[1].Bullets[0] = strings.Repeat("word ", 60)
	_, err := Check(deck, Options{MinSlides: 3, MaxSlides: 30})
	if err == nil || !strings.Contains(err.Error(), "slide 2 (bullets) bullet 1 is 43 words; keep it to 25") {
		t.Errorf("err %v", err)
	}
	deck = validDeck()
	deck.Slides[1].Bullets[1] = "**Bold** in the **middle**"
	if _, err := Check(deck, Options{MinSlides: 3, MaxSlides: 30}); err == nil || !strings.Contains(err.Error(), "bold is one lead phrase") {
		t.Errorf("err %v", err)
	}
	deck = validDeck()
	deck.Slides[0].Subtitle = "a\x07b"
	if _, err := Check(deck, Options{MinSlides: 3, MaxSlides: 30}); err == nil || !strings.Contains(err.Error(), "control character (U+0007)") {
		t.Errorf("err %v", err)
	}
}

func TestAFieldTheSchemaDoesNotHaveIsRefused(t *testing.T) {
	if _, err := Decode([]byte(`{"title":"T","theme":"clinical","slides":[{"kind":"bullets","image":"x.png"}]}`)); err == nil || !strings.Contains(err.Error(), `unknown field "image"`) {
		t.Errorf("err %v", err)
	}
	if _, err := Decode([]byte(`{"title":"T"} {"title":"U"}`)); err == nil {
		t.Error("two decks were read as one")
	}
}

func TestAVideoDeckIsNarratedPartByPart(t *testing.T) {
	deck := validDeck()
	deck.Sources = nil
	deck.Slides[1].Cites = nil
	options := Options{MinSlides: 3, MaxSlides: 30, Narrated: true}
	if _, err := Check(deck, options); err == nil || !strings.Contains(err.Error(), "slide 1 (title) has 0 narration passages and 2 parts") {
		t.Errorf("unnarrated: %v", err)
	}
	// One passage per part, exactly: a passage over, a passage short (the
	// parts the old check let come in evenly across the last one), and a
	// slide that has it right.
	deck.Slides[0].Narration = []string{"Welcome.", "Plants make food.", "One too many."}
	deck.Slides[1].Narration = []string{"Two things go in."}
	deck.Slides[2].Narration = []string{"Light first.", "Then water.", "Then ATP.", "And done."}
	_, err := Check(deck, options)
	for _, want := range []string{
		"slide 1 (title) has 3 narration passages and 2 parts; give exactly 2 narration passages, one as it appears and then one for its subtitle",
		"slide 2 (bullets) has 1 narration passages and 3 parts; give exactly 3 narration passages, one as it appears and then one for each of its 2 bullets",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("no %q in %v", want, err)
		}
	}
	if err != nil && strings.Contains(err.Error(), "slide 3") {
		t.Errorf("a slide narrated part by part was refused: %v", err)
	}
	deck.Slides[0].Narration = []string{"Welcome.", "Plants make food."}
	deck.Slides[1].Narration = []string{"Two things go in.", "Carbon dioxide and water.", "Glucose and oxygen come out."}
	if _, err := Check(deck, options); err != nil {
		t.Errorf("a deck narrated part by part: %v", err)
	}
	// A slide that comes in whole is told it has one passage.
	section := Slide{Kind: KindSection, Title: "Part two", Narration: []string{"Now the dark reactions.", "They fix carbon."}}
	var p Problems
	checkNarration(&p, &section, "slide 4 (section)", true)
	if want := "slide 4 (section) has 2 narration passages and 1 parts; give exactly 1 narration passage, spoken as it appears"; len(p) != 1 || p[0] != want {
		t.Errorf("section: %v", p)
	}
	// Narration in a deck that is not a video's is a mistake, said so; a
	// deck file has none and needs none.
	plain := validDeck()
	if _, err := Check(plain, Options{MinSlides: 3, MaxSlides: 30}); err != nil {
		t.Errorf("a deck file without narration: %v", err)
	}
	plain.Slides[0].Narration = []string{"Hello."}
	if _, err := Check(plain, Options{MinSlides: 3, MaxSlides: 30}); err == nil || !strings.Contains(err.Error(), "only a video's slides have") {
		t.Errorf("err %v", err)
	}
	withSources := validDeck()
	if _, err := Check(withSources, options); err == nil || !strings.Contains(err.Error(), "a video's deck lists no sources") {
		t.Errorf("err %v", err)
	}
}

// Every kind's refusal names a count that is its Steps: the words a model
// is told and the number it is held to cannot drift apart.
func TestThePartsNamedAreTheStepsCounted(t *testing.T) {
	slides := []Slide{
		{Kind: KindTitle}, {Kind: KindTitle, Subtitle: "s"}, {Kind: KindSection},
		{Kind: KindBullets, Bullets: []string{"a", "b", "c"}},
		{Kind: KindTwoColumn}, {Kind: KindComparison, Rows: []Row{{}, {}}},
		{Kind: KindQuote}, {Kind: KindQuote, Attribution: "a"},
		{Kind: KindTimeline, Events: []Event{{}, {}, {}, {}}}, {Kind: KindStats, Stats: []Stat{{}, {}}},
		{Kind: KindDiagram, Steps: []Step{{}, {}, {}}}, {Kind: KindChart, Bars: []Bar{{}, {}, {}}},
	}
	for _, s := range slides {
		named := partsNamed(s)
		if (Steps(s) == 1) != (named == "") {
			t.Errorf("%s: %d steps, parts %q", s.Kind, Steps(s), named)
		}
		var number int
		if _, err := fmt.Sscanf(named, "one for each of its %d", &number); err == nil && number != Steps(s)-1 {
			t.Errorf("%s: %d steps, parts %q", s.Kind, Steps(s), named)
		}
	}
	if Spoken(Slide{Narration: []string{"one two", "three"}}) != "one two three" {
		t.Error(Spoken(Slide{Narration: []string{"one two", "three"}}))
	}
}

func TestTheSchemaOffersExactlyTheKindsAndThemes(t *testing.T) {
	var schema struct {
		Properties struct {
			Theme  struct{ Enum []string }
			Slides struct {
				Items struct {
					Properties map[string]json.RawMessage
					Required   []string
				}
			}
		}
	}
	if err := json.Unmarshal(Schema(true), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties.Theme.Enum) != len(Themes) {
		t.Errorf("themes %v", schema.Properties.Theme.Enum)
	}
	var kind struct{ Enum []string }
	_ = json.Unmarshal(schema.Properties.Slides.Items.Properties["kind"], &kind)
	if len(kind.Enum) != len(Kinds) || strings.Join(schema.Properties.Slides.Items.Required, ",") != "kind,narration" {
		t.Errorf("kinds %v, required %v", kind.Enum, schema.Properties.Slides.Items.Required)
	}
	if _, narrated := schema.Properties.Slides.Items.Properties["narration"]; !narrated {
		t.Error("a video's schema has no narration")
	}
	if strings.Contains(string(Schema(false)), "narration") {
		t.Error("a deck's schema offers narration")
	}
	for _, k := range Kinds {
		if !strings.Contains(Instructions(false), "- "+string(k)+":") {
			t.Errorf("the instructions do not describe %s", k)
		}
	}
}

func TestTheMarkdownHasEveryWord(t *testing.T) {
	deck, err := Check(validDeck(), Options{MinSlides: 3, MaxSlides: 30})
	if err != nil {
		t.Fatal(err)
	}
	md := Markdown(deck)
	for _, want := range []string{"# Photosynthesis", "## 2. Inputs and outputs", "- **Inputs:** CO2 and water", "1. Light", "Sources: 1", "## Sources", "1. Campbell Biology. 2020"} {
		if !strings.Contains(md, want) {
			t.Errorf("no %q in\n%s", want, md)
		}
	}
	if FormatNumber(1234567.5) != "1,234,567.5" || FormatNumber(12) != "12" || FormatNumber(0.25) != "0.25" {
		t.Error(FormatNumber(1234567.5), FormatNumber(12), FormatNumber(0.25))
	}
}
