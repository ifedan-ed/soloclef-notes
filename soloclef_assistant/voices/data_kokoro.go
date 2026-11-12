package voices

// kokoroVoices is the roster of a hosted Kokoro model.
//
// A Kokoro voice's name says what it is: the first letter is the language
// (a American English, b British, e Spanish, f French, h Hindi, i Italian,
// j Japanese, p Brazilian Portuguese, z Mandarin) and the second is f or m.
// That is why a Kokoro model must never be read as an OpenAI one: offered
// "coral", it refuses.
//
// The list is curated rather than complete: a male and a female voice for
// each language wherever Kokoro has both, so a two-speaker brief gets two
// voices a listener can tell apart. French has one.
//
// Reference: https://huggingface.co/hexgrad/Kokoro-82M/tree/main/voices
var kokoroVoices = []Voice{
	// American English
	kokoro("am_adam", "en-US", "Adam (US)", Male),
	kokoro("am_michael", "en-US", "Michael (US)", Male),
	kokoro("af_bella", "en-US", "Bella (US)", Female),
	kokoro("af_heart", "en-US", "Heart (US)", Female),
	kokoro("af_nicole", "en-US", "Nicole (US)", Female),
	kokoro("af_sarah", "en-US", "Sarah (US)", Female),
	// British English
	kokoro("bm_george", "en-GB", "George (UK)", Male),
	kokoro("bm_lewis", "en-GB", "Lewis (UK)", Male),
	kokoro("bf_emma", "en-GB", "Emma (UK)", Female),
	kokoro("bf_isabella", "en-GB", "Isabella (UK)", Female),
	// Spanish
	kokoro("em_alex", "es", "Alex (ES)", Male),
	kokoro("ef_dora", "es", "Dora (ES)", Female),
	// French
	kokoro("ff_siwis", "fr", "Siwis (FR)", Female),
	// Hindi
	kokoro("hm_omega", "hi", "Omega (HI)", Male),
	kokoro("hf_alpha", "hi", "Alpha (HI)", Female),
	// Italian
	kokoro("im_nicola", "it", "Nicola (IT)", Male),
	kokoro("if_sara", "it", "Sara (IT)", Female),
	// Japanese
	kokoro("jm_kumo", "ja", "Kumo (JA)", Male),
	kokoro("jf_alpha", "ja", "Alpha (JA)", Female),
	// Brazilian Portuguese
	kokoro("pm_alex", "pt-BR", "Alex (BR)", Male),
	kokoro("pf_dora", "pt-BR", "Dora (BR)", Female),
	// Mandarin Chinese
	kokoro("zm_yunxi", "zh", "Yunxi (ZH)", Male),
	kokoro("zf_xiaoxiao", "zh", "Xiaoxiao (ZH)", Female),
}

func kokoro(name, language, display string, gender Gender) Voice {
	return Voice{
		ID: string(Kokoro) + ":" + name, Language: language,
		DisplayName: display, Gender: gender, Native: name,
	}
}
