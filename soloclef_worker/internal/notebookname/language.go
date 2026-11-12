package notebookname

import (
	"strings"
	"unicode"
)

// The language a title must be written in, and a check that it was
// (app/quick_tasks/language.py). A quick model told only "in the
// material's language" drifted into Chinese on an English paper, so the
// prompt names the language outright, and a reply in the wrong script is
// asked for once more.
//
// Scripts are told apart by their letters. Within the Latin script the
// Python tier asked langdetect; here the common short words of each
// language this app's users write in are counted, which is all a title's
// language needs, and English is the answer when nothing stands out.

const defaultLanguage = "en"

var languageNames = map[string]string{
	"en": "English", "es": "Spanish", "pt": "Portuguese", "hi": "Hindi", "zh": "Chinese",
	"ko": "Korean", "ja": "Japanese", "fr": "French", "de": "German", "it": "Italian",
	"nl": "Dutch", "ru": "Russian", "uk": "Ukrainian", "ar": "Arabic", "tr": "Turkish",
	"pl": "Polish", "sv": "Swedish", "vi": "Vietnamese",
}

func languageName(code string) string {
	if name, known := languageNames[code]; known {
		return name
	}
	return code
}

// minLetters is the least text a language can be told from; a short title
// in the wrong script is still caught (minScriptLetters).
const (
	minLetters       = 12
	minScriptLetters = 4
)

type script string

const (
	latin      script = "latin"
	cjk        script = "cjk"
	hangul     script = "hangul"
	devanagari script = "devanagari"
	cyrillic   script = "cyrillic"
	arabic     script = "arabic"
)

func scriptOf(r rune) (script, bool) {
	if !unicode.IsLetter(r) {
		return "", false
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

func scriptCounts(text string) map[script]int {
	counts := map[script]int{}
	for _, r := range text {
		if s, isLetter := scriptOf(r); isLetter {
			counts[s]++
		}
	}
	return counts
}

// expectedScript is the script a language is written in.
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

func latinLanguage(text string) string {
	counts := map[string]int{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		for language, common := range latinWords {
			for _, candidate := range common {
				if word == candidate {
					counts[language]++
				}
			}
		}
	}
	best, bestCount := defaultLanguage, 0
	for language, count := range counts {
		// A tie keeps English: it is the default, and the stable choice.
		if count > bestCount || (count == bestCount && language == defaultLanguage) {
			best, bestCount = language, count
		}
	}
	return best
}

// detectLanguage is the main language of a text, or "" when there is too
// little of it to tell.
func detectLanguage(text string) string {
	sample := firstRunes(strings.TrimSpace(text), 5000)
	counts := scriptCounts(sample)
	letters := 0
	for _, n := range counts {
		letters += n
	}
	if letters < minLetters {
		return ""
	}
	// A CJK character carries a word; weigh them against Latin letters.
	dominant, weight := latin, -1
	for _, s := range []script{latin, cjk, hangul, devanagari, cyrillic, arabic} {
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

// targetLanguage is the material's language, else English.
func targetLanguage(material string) string {
	if detected := detectLanguage(material); detected != "" {
		return detected
	}
	return defaultLanguage
}

func languageInstruction(code string) string {
	name := languageName(code)
	return "Write your entire reply in " + name + ", whatever language the material is in. Do not switch to any other language."
}

func retryInstruction(code string) string {
	name := languageName(code)
	return "Your previous reply was not in " + name + ". Reply again, in " + name + " only: every sentence, heading and item in " + name + "."
}

// inLanguage reports whether text is written in the language's script; a
// few foreign terms are fine.
func inLanguage(text, code string) bool {
	counts := scriptCounts(text)
	total := 0
	for _, n := range counts {
		total += n
	}
	if total < minScriptLetters {
		return true
	}
	expected, listed := expectedScript[code]
	if !listed {
		expected = latin
	}
	if expected == latin {
		return float64(total-counts[latin])/float64(total) < 0.1
	}
	return float64(counts[expected])/float64(total) >= 0.3
}
