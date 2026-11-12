package studioviews

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// The language a report is written in, as a two- or three-letter code. The
// suggestions are written in it and an added output is asked for in it, so
// a French report is not offered English flashcards.
//
// It is the language the report was asked for in when its options name one,
// and otherwise the language of its text. Scripts are told apart by their
// letters. Within the Latin script the commonest short words of each
// language are counted, which is the method the worker names notebooks by
// (soloclef_worker/internal/notebookname; a separate module's internal
// package, so it cannot be imported). English is the answer when nothing
// stands out.

const defaultLanguage = "en"

var languageNames = map[string]string{
	"en": "English", "es": "Spanish", "pt": "Portuguese", "hi": "Hindi", "zh": "Chinese",
	"ko": "Korean", "ja": "Japanese", "fr": "French", "de": "German", "it": "Italian",
	"nl": "Dutch", "ru": "Russian", "uk": "Ukrainian", "ar": "Arabic", "tr": "Turkish",
	"pl": "Polish", "sv": "Swedish", "vi": "Vietnamese",
}

// languageName is the language as a prompt names it; an unlisted code is
// named by itself.
func languageName(code string) string {
	if name, known := languageNames[code]; known {
		return name
	}
	return code
}

var languageTag = regexp.MustCompile(`^([A-Za-z]{2,3})(?:[-_][A-Za-z0-9]{1,8})*$`)

// askedLanguage is the code for what a dialog stored as the language: a
// name ("French") or a tag ("pt-BR"). "" when it is neither.
func askedLanguage(asked string) string {
	asked = strings.TrimSpace(asked)
	for code, name := range languageNames {
		if strings.EqualFold(name, asked) {
			return code
		}
	}
	if match := languageTag.FindStringSubmatch(asked); match != nil {
		return strings.ToLower(match[1])
	}
	return ""
}

// reportLanguage is the language the report was asked for in, else the
// language of its text.
func reportLanguage(asked, markdown string) string {
	if code := askedLanguage(asked); code != "" {
		return code
	}
	return detectLanguage(markdown)
}

type script int

const (
	latin script = iota
	cjk
	hangul
	devanagari
	cyrillic
	arabic
	scripts
)

func scriptOf(r rune) (script, bool) {
	if !unicode.IsLetter(r) {
		return 0, false
	}
	switch {
	case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana):
		return cjk, true
	case unicode.Is(unicode.Hangul, r):
		return hangul, true
	case unicode.Is(unicode.Devanagari, r):
		return devanagari, true
	case unicode.Is(unicode.Cyrillic, r):
		return cyrillic, true
	case unicode.Is(unicode.Arabic, r):
		return arabic, true
	}
	return latin, true
}

func scriptCounts(text string) (counts [scripts]int, total int) {
	for _, r := range text {
		if s, isLetter := scriptOf(r); isLetter {
			counts[s]++
			total++
		}
	}
	return counts, total
}

// expectedScript is the script a language is written in; unlisted is Latin.
var expectedScript = map[string]script{"zh": cjk, "ja": cjk, "ko": hangul, "hi": devanagari, "ru": cyrillic, "uk": cyrillic, "ar": arabic}

// latinWords are each language's commonest short words, the ones a
// paragraph of it cannot avoid.
var latinWords = map[string][]string{
	"en": {"the", "and", "of", "to", "is", "in", "that", "with", "for", "are", "this", "which"},
	"es": {"el", "la", "los", "las", "de", "que", "y", "en", "del", "por", "para", "con", "una", "es"},
	"pt": {"o", "a", "os", "as", "de", "que", "e", "em", "do", "da", "para", "com", "uma", "não", "são"},
	"fr": {"le", "la", "les", "de", "des", "et", "est", "en", "du", "que", "une", "pour", "dans", "pas"},
	"de": {"der", "die", "das", "und", "ist", "nicht", "mit", "von", "den", "ein", "eine", "zu", "auf"},
	"it": {"il", "la", "di", "che", "e", "è", "un", "una", "per", "non", "del", "della", "sono", "con"},
	"nl": {"de", "het", "een", "en", "van", "is", "dat", "niet", "zijn", "op", "voor", "met"},
	"pl": {"i", "w", "nie", "na", "się", "jest", "że", "z", "do", "to", "jak", "oraz"},
	"sv": {"och", "att", "det", "som", "en", "är", "av", "för", "med", "inte", "den", "till"},
	"tr": {"ve", "bir", "bu", "da", "de", "için", "ile", "olarak", "daha", "gibi", "çok"},
	"vi": {"và", "của", "là", "các", "có", "được", "trong", "cho", "những", "không", "một"},
}

// latinOwners is, for each such word, the languages it is common in; built
// once so a report's sample is one map lookup a word.
var latinOwners = func() map[string][]string {
	owners := map[string][]string{}
	for language, words := range latinWords {
		for _, word := range words {
			owners[word] = append(owners[word], language)
		}
	}
	return owners
}()

// latinOrder is the languages of latinWords in a fixed order.
var latinOrder = func() []string {
	order := make([]string, 0, len(latinWords))
	for language := range latinWords {
		order = append(order, language)
	}
	sort.Strings(order)
	return order
}()

func latinLanguage(text string) string {
	counts := map[string]int{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		for _, language := range latinOwners[word] {
			counts[language]++
		}
	}
	// English unless another language has strictly more, tried in a fixed
	// order, so that a tie never depends on the order a map is walked in.
	best, most := defaultLanguage, counts[defaultLanguage]
	for _, language := range latinOrder {
		if counts[language] > most {
			best, most = language, counts[language]
		}
	}
	return best
}

// detectLanguage is the main language of a text; English when there is too
// little of it to tell.
func detectLanguage(text string) string {
	sample := strings.TrimSpace(text)
	if runes := []rune(sample); len(runes) > 5000 {
		sample = string(runes[:5000])
	}
	counts, total := scriptCounts(sample)
	// Fewer letters than this cannot be told apart by language.
	if total < 12 {
		return defaultLanguage
	}
	// A CJK character carries a word; weigh them against Latin letters.
	dominant, weight := latin, -1
	for s := latin; s < scripts; s++ {
		w := counts[s]
		if s == cjk || s == hangul {
			w *= 3
		}
		if w > weight {
			dominant, weight = s, w
		}
	}
	switch dominant {
	case cjk:
		for _, r := range sample {
			if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
				return "ja"
			}
		}
		return "zh"
	case hangul:
		return "ko"
	case devanagari:
		return "hi"
	case arabic:
		return "ar"
	case cyrillic:
		// Ukrainian has four letters Russian does not.
		if strings.ContainsAny(sample, "іїєґІЇЄҐ") {
			return "uk"
		}
		return "ru"
	}
	return latinLanguage(sample)
}

// writtenIn reports whether text is in the language's script; a few foreign
// terms are fine, and a text of under four letters cannot be judged.
func writtenIn(text, code string) bool {
	counts, total := scriptCounts(text)
	if total < 4 {
		return true
	}
	expected := expectedScript[code] // the zero value is latin
	if expected == latin {
		return float64(total-counts[latin])/float64(total) < 0.1
	}
	return float64(counts[expected])/float64(total) >= 0.3
}
