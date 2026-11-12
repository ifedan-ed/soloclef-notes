package voices

// openAIVoices is every voice an OpenAI-style speech model may have. No one
// model has all of them, which is why this roster is narrowed by asking the
// proxy what the configured model speaks (catalogue.go): gpt-4o-mini-tts has
// the first thirteen, tts-1 and tts-1-hd nine of those, and xAI's Grok Voice
// the last five.
//
// Grok's voices carry the `openai:` prefix too. The prefix names the roster a
// voice is found in, and the proxy serves Grok on the same speech route.
//
// These voices follow the language of the text they are given, so each is
// tagged AnyLanguage.
var openAIVoices = []Voice{
	openAI("alloy", "Alloy", Neutral),
	openAI("ash", "Ash", Male),
	openAI("ballad", "Ballad", Male),
	openAI("coral", "Coral", Female),
	openAI("echo", "Echo", Male),
	openAI("fable", "Fable", Neutral),
	openAI("nova", "Nova", Female),
	openAI("onyx", "Onyx", Male),
	openAI("sage", "Sage", Female),
	openAI("shimmer", "Shimmer", Female),
	openAI("verse", "Verse", Male),
	openAI("marin", "Marin", Female),
	openAI("cedar", "Cedar", Male),
	// xAI Grok Voice TTS.
	openAI("ara", "Ara", Female),
	openAI("eve", "Eve", Female),
	openAI("leo", "Leo", Male),
	openAI("rex", "Rex", Male),
	openAI("sal", "Sal", Neutral),
}

func openAI(name, display string, gender Gender) Voice {
	return Voice{
		ID: string(OpenAI) + ":" + name, Language: AnyLanguage,
		DisplayName: display, Gender: gender, Native: name,
	}
}

// azureVoices is the roster of an Azure deployment fronted with OpenAI's
// voice names. Like those, each speaks any language.
var azureVoices = []Voice{
	azure("alloy", "Alloy", Neutral),
	azure("echo", "Echo", Male),
	azure("fable", "Fable", Neutral),
	azure("onyx", "Onyx", Male),
	azure("nova", "Nova", Female),
	azure("shimmer", "Shimmer", Female),
}

func azure(name, display string, gender Gender) Voice {
	return Voice{
		ID: string(Azure) + ":" + name, Language: AnyLanguage,
		DisplayName: display, Gender: gender, Native: name,
	}
}
