package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// A podcast is not saved the way the other kinds are. The specialist does
// not write the episode: it opens a podcast, which is a row waiting at its
// brief gate with a brief (language, voices, length) already proposed. The
// page's podcast card then shows that brief as a form in the chat; the
// person approves it there, and only that approval queues the transcript
// draft (internal/podcasts, the approve route). So opening a podcast asks no
// model, queues nothing and costs nothing, and this file is the whole of
// what chat does for one.

// PodcastTool is keyed to the page's podcast card.
const PodcastTool = "generate_podcast"

// PodcastRequest is a podcast to open: what the specialist's model supplied.
type PodcastRequest struct {
	Title string
	// Source is the text the episode will be drafted from.
	Source string
	// Focus is what the episode should dwell on, or "".
	Focus string
	// Language is the language the episode is to be spoken in, a tag as
	// the roster offers them (voices.Roster.Languages), or "" for none
	// asked: the account's last choice then, as propose says. The studio's
	// audio dialog sets it; the chat's tool does not.
	Language string
}

// PodcastOpener opens a podcast at its brief gate for the turn's owner and
// answers the new podcast's id. It is an interface so that the specialist
// is tested without a database or a voice catalogue.
//
// An error that is a *PodcastRefusal carries a sentence the person may be
// told. Any other error is a failure whose text stays in the log.
type PodcastOpener interface {
	// Check reports what is missing. Called once at start-up.
	Check() error
	Open(ctx context.Context, owner Owner, request PodcastRequest) (podcastID int64, err error)
}

// PodcastRefusal is a podcast that was not opened for a reason the person
// asking can do something about, or at least should hear.
type PodcastRefusal struct{ Reason string }

func (r *PodcastRefusal) Error() string { return r.Reason }

// What a person is told when a podcast cannot be opened.
const (
	noSpeechService     = "This server has no speech service set up, so podcasts cannot be made here."
	voicesUnavailable   = "The podcast voices could not be loaded just now. Try again in a moment."
	voicesMisconfigured = "Podcast voices are not set up correctly on this server."
	noVoiceForLanguage  = "No podcast voice on this server speaks the language you chose. Choose another language."
)

// The bounds of a podcast's title, focus and brief. They are the ones the
// API tier's internal/podcasts holds a new podcast and a stored brief to (requests.go,
// spec.go): a row written past them is one its own routes cannot read back.
const (
	maxPodcastTitleChars = 500
	maxFocusChars        = 2000
	maxPodcastSpeakers   = 6
	minPodcastSeconds    = 15
	maxPodcastSeconds    = 24 * 60 * 60
)

// PodcastBrief is the part of a proposed brief that is this deployment's
// choice rather than the account's history: how many voices and how long.
// Every field is required. The person sees all three on the card and can
// change them before anything is drafted.
type PodcastBrief struct {
	Speakers   int
	MinSeconds int
	MaxSeconds int
}

// VoiceCatalogue is the voices the deployment's speech service has
// (*voices.Catalogue). It is an interface so that a test can name a roster
// without a model proxy.
type VoiceCatalogue interface {
	Roster(ctx context.Context) (*voices.Roster, error)
}

// PodcastRows is the podcast table as opening a podcast needs it.
type PodcastRows interface {
	// Create writes a podcast at the brief gate for the owner's own account
	// and thread, and answers its id. propose is handed the brief of the
	// account's most recent podcast (the stored JSON, or nil when it has
	// none) and returns the new podcast's; the read and the insert are one
	// transaction, so a brief is never proposed from a row that a
	// concurrent delete has already removed.
	Create(ctx context.Context, owner Owner, title, source string, propose func(latest []byte) ([]byte, error)) (int64, error)
}

// PodcastStore is PodcastRows over the pool, with this package's own two
// statements (queries.sql). They are here, and not the API tier's podcast
// routes' statements, because every tier that runs the specialist opens
// podcasts and the routes' package cannot leave its tier; the API tier's
// database test opens one here and reads it back through its routes' own
// reader, which is what holds the two to one idea of a podcast row.
type PodcastStore struct {
	Pool *pgxpool.Pool
}

func (s PodcastStore) Create(ctx context.Context, owner Owner, title, source string, propose func(latest []byte) ([]byte, error)) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		latest, err := queries.LatestPodcastBrief(ctx, owner.UserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		spec, err := propose(latest)
		if err != nil {
			return err
		}
		id, err = queries.CreatePodcast(ctx, CreatePodcastParams{
			Title: title, SourceContent: source, Spec: spec, ThreadID: owner.ThreadID, UserID: owner.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The statement takes the thread from the owner's own threads
			// and inserted nothing: the thread is gone or is not theirs.
			return errors.New("deliverables: the podcast's thread is not one of its account's")
		}
		return err
	})
	return id, err
}

// Podcasts is PodcastOpener over the podcast table and the voice roster.
//
// What it cannot share with the API tier's podcast routes is the proposal
// of the brief, which that package keeps unexported (proposal.go): propose
// below is the same rules written again, and the two should become one the
// day the routes can import this.
type Podcasts struct {
	Rows   PodcastRows
	Voices VoiceCatalogue
	Brief  PodcastBrief
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (p Podcasts) Check() error {
	switch {
	case p.Rows == nil:
		return errors.New("deliverables: Podcasts.Rows is not set")
	case p.Voices == nil:
		return errors.New("deliverables: Podcasts.Voices is not set")
	case p.Logger == nil:
		return errors.New("deliverables: Podcasts.Logger is not set")
	case p.Brief.Speakers < 1 || p.Brief.Speakers > maxPodcastSpeakers:
		return fmt.Errorf("deliverables: Podcasts.Brief.Speakers must be 1 to %d, got %d", maxPodcastSpeakers, p.Brief.Speakers)
	case p.Brief.MinSeconds < minPodcastSeconds || p.Brief.MaxSeconds > maxPodcastSeconds || p.Brief.MaxSeconds < p.Brief.MinSeconds:
		return fmt.Errorf("deliverables: Podcasts.Brief needs %d <= MinSeconds <= MaxSeconds <= %d, got %d and %d",
			minPodcastSeconds, maxPodcastSeconds, p.Brief.MinSeconds, p.Brief.MaxSeconds)
	}
	return nil
}

// Open creates the podcast row, at the brief gate, with a brief proposed.
func (p Podcasts) Open(ctx context.Context, owner Owner, request PodcastRequest) (int64, error) {
	// Asked before the transaction, because it can mean a request to the
	// model proxy and a transaction must not be held open across one.
	roster, err := p.Voices.Roster(ctx)
	if err != nil {
		return 0, p.voicesError(ctx, err)
	}

	// For the turn's own account and thread: both were fixed by the server
	// when the turn was built, and the insert names both.
	id, err := p.Rows.Create(ctx, owner, request.Title, request.Source, func(latest []byte) ([]byte, error) {
		brief, err := propose(roster, lastChoicesIn(latest), p.Brief, request.Focus, request.Language)
		if err != nil {
			return nil, err
		}
		return encodeBrief(brief)
	})
	if err != nil {
		return 0, p.voicesError(ctx, err)
	}
	return id, nil
}

// voicesError turns the voice catalogue's three ways of not answering into
// refusals, and passes every other error through. They are told apart
// because they are fixed by different people: nobody, by waiting, and the
// operator.
func (p Podcasts) voicesError(ctx context.Context, err error) error {
	var unreachable *voices.UnreachableError
	var wrong *voices.MisconfiguredError
	switch {
	case errors.Is(err, voices.ErrNotConfigured):
		return &PodcastRefusal{Reason: noSpeechService}
	case errors.As(err, &unreachable):
		return &PodcastRefusal{Reason: voicesUnavailable}
	case errors.As(err, &wrong):
		// The person is told it is the server; the operator is told which
		// setting.
		p.Logger.LogAttrs(ctx, slog.LevelError, "a podcast could not be opened from chat: the voices are misconfigured", slog.String("error", err.Error()))
		return &PodcastRefusal{Reason: voicesMisconfigured}
	}
	return err
}

// lastChoices is what the account chose for its most recent podcast: the
// language, and each speaker's voice in the order the brief lists them.
type lastChoices struct {
	language string
	voices   []string
}

// lastChoicesIn reads the choices out of a stored brief (the jsonb column's
// JSON). Only the two things a proposal uses are read. A brief that does
// not have them in the expected places has no choices to offer, and the new
// podcast starts from the roster's defaults: an old row that cannot be read
// is no reason to refuse a new podcast.
func lastChoicesIn(column []byte) lastChoices {
	var brief struct {
		Language json.RawMessage `json:"language"`
		Speakers json.RawMessage `json:"speakers"`
	}
	if len(column) == 0 || json.Unmarshal(column, &brief) != nil {
		return lastChoices{}
	}
	// Each is read on its own, so a language that is not text or a speakers
	// that is not a list is simply absent.
	var language string
	_ = json.Unmarshal(brief.Language, &language)
	var speakers []json.RawMessage
	_ = json.Unmarshal(brief.Speakers, &speakers)
	choices := lastChoices{language: strings.TrimSpace(language)}
	for _, entry := range speakers {
		var speaker struct {
			VoiceID json.RawMessage `json:"voice_id"`
		}
		var voice string
		if json.Unmarshal(entry, &speaker) != nil || json.Unmarshal(speaker.VoiceID, &voice) != nil || voice == "" {
			return lastChoices{}
		}
		choices.voices = append(choices.voices, voice)
	}
	return choices
}

// proposedBrief is a new podcast's brief as it is stored: the keys the
// podcast routes read a brief by (the API tier's internal/podcasts,
// spec.go).
type proposedBrief struct {
	Language string            `json:"language"`
	Style    string            `json:"style"`
	Speakers []proposedSpeaker `json:"speakers"`
	Duration struct {
		MinSeconds int `json:"min_seconds"`
		MaxSeconds int `json:"max_seconds"`
	} `json:"duration"`
	// Focus is null for a podcast given no steer.
	Focus *string `json:"focus"`
}

type proposedSpeaker struct {
	Slot    int    `json:"slot"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	VoiceID string `json:"voice_id"`
	// Delivery is a note written by voice casting, later, if at all.
	Delivery *string `json:"delivery"`
}

// encodeBrief is the brief as the column takes it. A person's focus is kept
// as they wrote it: "<" is not turned into an escape nobody typed.
func encodeBrief(brief proposedBrief) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(brief); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// The fixed parts of a proposed brief (internal/podcasts, proposal.go and
// spec.go): English unless the account last chose otherwise, a
// conversation, and speakers named for their roles.
const (
	defaultPodcastLanguage = "en"
	defaultPodcastStyle    = "conversational"
)

// podcastRoles is the role each speaker is given, by slot, and what a
// speaker in it is called until the person names them.
var podcastRoles = []struct{ role, name string }{
	{"host", "Host"}, {"guest", "Guest"}, {"expert", "Expert"}, {"cohost", "Co-Host"}, {"narrator", "Narrator"},
}

// propose writes the brief of a new podcast. Every choice in it is one the
// person can change on the card. Each rule has exactly one outcome:
//
//   - Language: the language asked for, when one was (the studio dialog's
//     choice), and a refusal if no voice speaks it: a podcast in another
//     language than the one chosen is not what was asked. With none
//     asked, the language of the account's last brief if the roster still
//     has a voice for it, otherwise English. The source text is never
//     examined to guess one.
//   - Voices, speaker by speaker: the voice that speaker had last time;
//     failing that the first configured host nobody has yet; failing that a
//     voice nobody has yet, preferring a gender nobody has, so that two
//     speakers sound like two people.
func propose(roster *voices.Roster, last lastChoices, shape PodcastBrief, focus, asked string) (proposedBrief, error) {
	language := defaultPodcastLanguage
	switch {
	case asked != "" && !roster.Supports(asked):
		return proposedBrief{}, &PodcastRefusal{Reason: noVoiceForLanguage}
	case asked != "":
		// The voices follow it: available and the hosts below are that
		// language's, and a voice the account used last time is kept only
		// if it is one of them (castVoices).
		language = asked
	case last.language != "" && roster.Supports(last.language):
		language = last.language
	}
	available := roster.ForLanguage(language)
	if len(available) == 0 {
		return proposedBrief{}, &voices.MisconfiguredError{
			Setting: voices.ServiceVariable,
			Reason:  "names a service with no voice for the default language, " + defaultPodcastLanguage,
		}
	}
	cast := castVoices(available, last.voices, roster.Hosts(language), shape.Speakers)
	brief := proposedBrief{Language: language, Style: defaultPodcastStyle, Speakers: make([]proposedSpeaker, 0, len(cast))}
	for slot, voice := range cast {
		// A sixth speaker is a guest, numbered.
		role, name := "guest", "Guest "+strconv.Itoa(slot)
		if slot < len(podcastRoles) {
			role, name = podcastRoles[slot].role, podcastRoles[slot].name
		}
		brief.Speakers = append(brief.Speakers, proposedSpeaker{Slot: slot, Name: name, Role: role, VoiceID: voice.ID})
	}
	brief.Duration.MinSeconds, brief.Duration.MaxSeconds = shape.MinSeconds, shape.MaxSeconds
	if focus != "" {
		brief.Focus = &focus
	}
	return brief, nil
}

// castVoices gives each of count speakers a voice out of available, the
// voices that speak the brief's language. When there are more speakers than
// voices the first voice is used again: every speaker gets a voice, and the
// person sees the repeat on the card and can change the language or the
// cast.
func castVoices(available []voices.Voice, lastUsed []string, hosts []voices.Voice, count int) []voices.Voice {
	byID := make(map[string]voices.Voice, len(available))
	for _, voice := range available {
		byID[voice.ID] = voice
	}
	cast := make([]voices.Voice, 0, count)
	taken := map[string]bool{}
	heard := map[voices.Gender]bool{}
	for slot := 0; slot < count; slot++ {
		var chosen voices.Voice
		if slot < len(lastUsed) && !taken[lastUsed[slot]] {
			// The zero Voice if last time's is not among the available.
			chosen = byID[lastUsed[slot]]
		}
		if chosen.ID == "" {
			for _, host := range hosts {
				if !taken[host.ID] {
					chosen = host
					break
				}
			}
		}
		if chosen.ID == "" {
			chosen = unusedVoice(available, taken, heard)
		}
		cast = append(cast, chosen)
		taken[chosen.ID] = true
		heard[chosen.Gender] = true
	}
	return cast
}

// unusedVoice is the first voice nobody has whose gender nobody has; failing
// that the first voice nobody has; failing that the first voice.
func unusedVoice(available []voices.Voice, taken map[string]bool, heard map[voices.Gender]bool) voices.Voice {
	spare := -1
	for index, voice := range available {
		if taken[voice.ID] {
			continue
		}
		if !heard[voice.Gender] {
			return voice
		}
		if spare < 0 {
			spare = index
		}
	}
	if spare >= 0 {
		return available[spare]
	}
	return available[0]
}

// newPodcastRequest checks what the model passed for a podcast and returns
// the request, or every problem found.
func newPodcastRequest(title, source, focus string) (PodcastRequest, error) {
	var problems []string
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		problems = append(problems, "podcast_title is empty")
	case utf8.RuneCountInString(title) > maxPodcastTitleChars:
		problems = append(problems, fmt.Sprintf("podcast_title is longer than %d characters", maxPodcastTitleChars))
	case strings.ContainsAny(title, "\r\n"):
		problems = append(problems, "podcast_title must be one line")
	}
	if bad := badCharacter(title); bad != "" {
		problems = append(problems, "podcast_title contains "+bad)
	}
	source = sourceText(source)
	if source == "" {
		problems = append(problems, "source_content is empty; it is the material the episode is made from, copied in full")
	}
	focus = strings.TrimSpace(focus)
	if utf8.RuneCountInString(focus) > maxFocusChars {
		problems = append(problems, fmt.Sprintf("user_prompt is longer than %d characters; it is a short steer, and the material belongs in source_content", maxFocusChars))
	}
	if bad := badCharacter(focus); bad != "" {
		problems = append(problems, "user_prompt contains "+bad)
	}
	if len(problems) > 0 {
		return PodcastRequest{}, refusal("the podcast was not created", problems)
	}
	return PodcastRequest{Title: title, Source: source, Focus: focus}, nil
}

// sourceText is a podcast's source as it is stored: line endings made one
// kind, and control characters removed.
//
// The other kinds refuse a control character and have the model send the
// field again. A source is not a field a model typed: it is pages of the
// person's own material copied through, a stray form feed in a PDF's text is
// ordinary, and refusing would cost the whole copy a second time to remove
// a character no listener could hear. Postgres cannot store U+0000 in text
// at all, so that one has to go either way.
func sourceText(source string) string {
	source = lineEndings.Replace(source)
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == byteOrderMark {
			return -1
		}
		return r
	}, source))
}
