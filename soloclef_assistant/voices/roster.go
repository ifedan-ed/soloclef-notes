package voices

import (
	"sort"
	"strings"
)

// Roster is the voices the configured service has, and which of them the
// deployment chose as hosts. It is read-only once built, so one roster is
// shared by every request.
type Roster struct {
	name   RosterName
	voices []Voice
	byID   map[string]Voice
	// hosts is Settings.PodcastVoices as voices, in the order written.
	hosts []Voice
}

// newRoster indexes the voices and resolves the configured hosts against
// them. A host the roster does not have is refused here, against the whole
// roster rather than the voices of one language: a host that does not speak
// the language of one podcast is a reason to pick another voice for that
// podcast, while a host the service has never had is a setting to correct.
func newRoster(name RosterName, voices []Voice, hostNames []string) (*Roster, error) {
	roster := &Roster{name: name, voices: voices, byID: make(map[string]Voice, len(voices))}
	for _, voice := range voices {
		roster.byID[voice.ID] = voice
	}
	var unknown []string
	chosen := map[string]bool{}
	for _, written := range hostNames {
		hostName := strings.TrimSpace(written)
		if hostName == "" {
			return nil, misconfigured(PodcastVoicesVariable, "has an empty entry; it is a comma-separated list of voice names")
		}
		id := string(name) + ":" + hostName
		if chosen[id] {
			// Two speakers given one voice by configuration is never what
			// was meant, and dropping the repeat would shift every later
			// speaker onto a voice nobody put in that position.
			return nil, misconfigured(PodcastVoicesVariable, "names %s twice", hostName)
		}
		chosen[id] = true
		voice, found := roster.byID[id]
		if !found {
			unknown = append(unknown, hostName)
			continue
		}
		roster.hosts = append(roster.hosts, voice)
	}
	if len(unknown) > 0 {
		return nil, misconfigured(PodcastVoicesVariable,
			"names %s, which the %s voices of %s do not include",
			strings.Join(unknown, ", "), name, ServiceVariable)
	}
	return roster, nil
}

// Name is the roster's name.
func (r *Roster) Name() RosterName { return r.name }

// Voices is every voice, in the order the picker shows them.
func (r *Roster) Voices() []Voice {
	return append([]Voice(nil), r.voices...)
}

// ForLanguage is the voices that can speak the language, in that same order.
func (r *Roster) ForLanguage(language string) []Voice {
	speaking := make([]Voice, 0, len(r.voices))
	for _, voice := range r.voices {
		if voice.Speaks(language) {
			speaking = append(speaking, voice)
		}
	}
	return speaking
}

// Voice is the voice with the id, if the roster has it. A voice of another
// roster is not found: a brief written for Kokoro names voices an OpenAI
// model cannot speak.
func (r *Roster) Voice(id string) (Voice, bool) {
	voice, found := r.byID[id]
	return voice, found
}

// Supports reports whether any voice speaks the language.
func (r *Roster) Supports(language string) bool {
	for _, voice := range r.voices {
		if voice.Speaks(language) {
			return true
		}
	}
	return false
}

// Hosts is the configured hosts that speak the language, in the order they
// were configured.
func (r *Roster) Hosts(language string) []Voice {
	speaking := make([]Voice, 0, len(r.hosts))
	for _, host := range r.hosts {
		if host.Speaks(language) {
			speaking = append(speaking, host)
		}
	}
	return speaking
}

// Offering is the languages the brief form may offer.
type Offering struct {
	// Languages is sorted.
	Languages []string
	// AllowsCustom says the list is a starting point: the roster has voices
	// that speak anything, so any language tag may be entered.
	AllowsCustom bool
}

// Languages is what the roster can offer up front. A voice bound to a
// language contributes its tag. A voice that speaks anything cannot name its
// languages, so one such voice adds the common list and opens free entry.
func (r *Roster) Languages() Offering {
	tags := map[string]bool{}
	anything := false
	for _, voice := range r.voices {
		if voice.Language == AnyLanguage {
			anything = true
			continue
		}
		tags[voice.Language] = true
	}
	if anything {
		for _, tag := range commonLanguages {
			tags[tag] = true
		}
	}
	offering := Offering{Languages: make([]string, 0, len(tags)), AllowsCustom: anything}
	for tag := range tags {
		offering.Languages = append(offering.Languages, tag)
	}
	sort.Strings(offering.Languages)
	return offering
}
