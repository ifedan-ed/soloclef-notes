package voices

// vertexVoices is Vertex AI's English Studio voices.
//
// A Vertex voice belongs to a locale and is named to the service by an
// object, `{"languageCode": …, "name": …}`, which is what Native holds. The
// British pair is the one place a voice's id and its native name differ:
// Vertex spells the locale "en-UK", and the id and the language tag use the
// BCP-47 "en-GB".
var vertexVoices = []Voice{
	vertex("en-US-Studio-O", "en-US", "en-US", "en-US-Studio-O", "Studio O (US)", Female),
	vertex("en-US-Studio-M", "en-US", "en-US", "en-US-Studio-M", "Studio M (US)", Male),
	vertex("en-GB-Studio-A", "en-GB", "en-UK", "en-UK-Studio-A", "Studio A (UK)", Female),
	vertex("en-GB-Studio-B", "en-GB", "en-UK", "en-UK-Studio-B", "Studio B (UK)", Male),
	vertex("en-AU-Studio-A", "en-AU", "en-AU", "en-AU-Studio-A", "Studio A (AU)", Female),
	vertex("en-AU-Studio-B", "en-AU", "en-AU", "en-AU-Studio-B", "Studio B (AU)", Male),
}

func vertex(key, language, locale, name, display string, gender Gender) Voice {
	return Voice{
		ID: string(Vertex) + ":" + key, Language: language,
		DisplayName: display, Gender: gender,
		Native: map[string]string{"languageCode": locale, "name": name},
	}
}
