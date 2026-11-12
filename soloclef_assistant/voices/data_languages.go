package voices

// commonLanguages is what the brief form offers up front for a roster whose
// voices speak any language. Such a roster has no list of languages to read
// out, so this is a starting point and not a limit: the form is told it may
// take any tag beyond these (Offering.AllowsCustom).
var commonLanguages = []string{
	"ar", "bn", "de", "en", "es", "fr", "hi", "id", "it", "ja", "ko",
	"nl", "pl", "pt", "ru", "sw", "th", "tr", "uk", "vi", "zh",
}
