package instructionslint

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// insensitive compiles a pattern that ignores case.
func insensitive(pattern string) *regexp.Regexp { return regexp.MustCompile(`(?i)` + pattern) }

// patterns are the fixed rules. Each states the intent itself ("don't
// cite", "no disclaimers") and none is a single word, so ordinary style
// guidance ("cite less often in summaries") does not trip one.
var patterns = map[Code][]*regexp.Regexp{
	Citations: {
		insensitive(`\b(?:no|without|skip|omit|drop|remove|disable|stop|hide)\s+` +
			`(?:any\s+|the\s+|all\s+)?(?:citations?|cites?|references?|footnotes?|\[n\])`),
		insensitive(`\b(?:don'?t|do\s+not|never)\s+(?:cite|include\s+citations?|add\s+citations?)\b`),
		insensitive(`\bcitations?\s+(?:off|disabled|are\s+not\s+needed|aren'?t\s+needed)\b`),
		insensitive(`\b(?:make\s+up|invent|fabricate)\s+(?:the\s+)?(?:citations?|references?|sources?)`),
	},
	OutsideKnowledge: {
		insensitive(`\b(?:use|rely\s+on|draw\s+on|answer\s+(?:with|from)|add|include)\s+(?:your\s+)?` +
			`(?:own|general|outside|external|prior|background|world|training)\s+knowledge`),
		insensitive(`\b(?:ignore|disregard|bypass)\s+(?:the\s+|my\s+|any\s+)?(?:sources?|documents?|notebook)`),
		insensitive(`\bnot\s+limited\s+to\s+(?:the\s+)?(?:sources?|documents?|notebook)`),
		insensitive(`\b(?:even\s+)?(?:if|when)\s+(?:it'?s|it\s+is|the\s+answer\s+is)?\s*not\s+in\s+the\s+` +
			`(?:sources?|documents?)[^.]{0,40}\b(?:answer\s+anyway|still\s+answer|make\s+(?:it|something)\s+up|guess|invent)`),
		insensitive(`\b(?:always|also|freely)\s+(?:search|browse)\s+(?:on\s+)?the\s+(?:web|internet)\b`),
	},
	Safety: {
		insensitive(`\b(?:ignore|bypass|disable|turn\s+off|remove|skip)\s+(?:all\s+|any\s+|the\s+|your\s+)?` +
			`(?:safety|guard\s*rails?|content\s+(?:policy|filters?)|restrictions?|refusals?)`),
		insensitive(`\b(?:never|don'?t|do\s+not)\s+(?:refuse|decline|say\s+no)\b`),
		insensitive(`\bwithout\s+(?:any\s+)?(?:warnings?|disclaimers?|caveats?|safety\s+(?:notes?|advice))`),
		insensitive(`\b(?:no|omit|skip|remove)\s+(?:the\s+|any\s+)?(?:medical\s+)?(?:disclaimers?|warnings?|caveats?)`),
		insensitive(`\b(?:uncensored|unfiltered|unrestricted)\b`),
	},
	PromptInjection: {
		insensitive(`\b(?:ignore|disregard|forget|override)\s+(?:all\s+|any\s+|the\s+|your\s+)?` +
			`(?:previous|prior|above|earlier|other|system|platform|original)\s+` +
			`(?:instructions?|rules?|prompts?|messages?)`),
		insensitive(`\b(?:reveal|print|show|repeat|leak)\s+(?:your\s+|the\s+)?(?:system\s+)?prompt\b`),
		insensitive(`\byou\s+are\s+(?:now|no\s+longer)\b`),
		insensitive(`\b(?:developer|god|jailbreak|dan)\s+mode\b`),
		insensitive(`\bjailbreak`),
		insensitive(`</?\s*[a-z_][a-z0-9_-]*\s*>`),
		insensitive(`\b(?:new|updated|real)\s+system\s+(?:prompt|instructions?)\b`),
	},
}

// Clean is the text as instructions are used: NULs gone, line endings
// alike, trimmed. Empty means there is nothing to check.
func Clean(text string) string {
	text = strings.ReplaceAll(text, "\x00", "")
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// ByRule is the pattern pass over cleaned text: at most one warning per
// code, quoting the first match of the first pattern that finds one.
func ByRule(cleaned string) []Warning {
	found := []Warning{}
	for _, code := range codes {
		for _, pattern := range patterns[code] {
			if match := pattern.FindStringIndex(cleaned); match != nil {
				found = append(found, Warning{Code: code, Quote: around(cleaned, match[0], match[1]), Source: "rule"})
				break
			}
		}
	}
	return found
}

// around is the match with up to quoteContext characters either side, on
// one line. The edges are moved a character at a time, never a byte, so a
// quote cannot begin or end inside a multi-byte character.
func around(text string, start, end int) string {
	for moved := 0; moved < quoteContext && start > 0; moved++ {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		start -= size
	}
	for moved := 0; moved < quoteContext && end < len(text); moved++ {
		_, size := utf8.DecodeRuneInString(text[end:])
		end += size
	}
	return oneLine(text[start:end])
}

// oneLine joins the words with single spaces and cuts the result at
// quoteChars characters.
func oneLine(text string) string {
	joined := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(joined) <= quoteChars {
		return joined
	}
	return string([]rune(joined)[:quoteChars])
}
