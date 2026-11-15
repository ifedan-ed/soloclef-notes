/**
 * The one kind of model connection the server supports, and the presets that
 * fill it in.
 *
 * Soloclef calls a person's own models itself, at the address they give, with
 * the key they give: GET {base}/models to list them and POST
 * {base}/chat/completions to use them (soloclef_assistant/ownmodels). Every
 * other model comes through the deployment's model proxy by name and is not
 * connected here. So there is exactly one provider, "openai_compatible", and
 * the server refuses any other.
 *
 * The grid used to offer Bedrock, Azure, Vertex AI, Ollama, LM Studio and
 * more, carried over from the Python app. Those connections were stored and
 * then never called by anything: the chat, the studio and the jobs open only
 * an OpenAI-compatible connection's models. They are gone. What stays are
 * presets: a known provider that speaks the OpenAI protocol at a public
 * address, whose only difference from "OpenAI-compatible" is that the base
 * URL is already typed. Each one was checked to answer both GET /models and
 * POST /chat/completions with a Bearer key at the address below.
 *
 * Ollama and LM Studio are not presets: they run on the operator's own
 * machine, and the server's egress guard refuses an address inside its own
 * network unless the operator has allowed that host. A person who has can
 * still use the OpenAI-compatible form with the server's address.
 */
export const OWN_PROVIDER = "openai_compatible";

export interface ConnectionPreset {
	/** Stable id, for keys and tests. Never sent to the server. */
	id: string;
	name: string;
	subtitle: string;
	/** lib/provider-icons key. */
	iconKey: string;
	/** The base URL the form starts with; empty for a custom endpoint. */
	baseUrl: string;
	/** What to say under the base URL field. */
	hint: string;
}

export const CUSTOM_PRESET: ConnectionPreset = {
	id: "custom",
	name: "OpenAI-Compatible",
	subtitle: "Any endpoint that speaks the OpenAI API",
	iconKey: "custom",
	baseUrl: "",
	hint: "The exact API base URL, used as it is. Most end in /v1 (https://api.example.com/v1). It must be a public address.",
};

export const CONNECTION_PRESETS: readonly ConnectionPreset[] = [
	{
		id: "openai",
		name: "GPT",
		subtitle: "OpenAI",
		iconKey: "openai",
		baseUrl: "https://api.openai.com/v1",
		hint: "OpenAI's API. Use a key from platform.openai.com.",
	},
	{
		id: "anthropic",
		name: "Claude",
		subtitle: "Anthropic, OpenAI-compatible API",
		iconKey: "claude",
		baseUrl: "https://api.anthropic.com/v1",
		hint: "Anthropic's OpenAI-compatible endpoint. Use a key from console.anthropic.com.",
	},
	{
		id: "gemini",
		name: "Gemini",
		subtitle: "Google AI Studio, OpenAI-compatible API",
		iconKey: "google",
		baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
		hint: "Google's OpenAI-compatible endpoint. Use a Gemini API key from aistudio.google.com.",
	},
	{
		id: "openrouter",
		name: "OpenRouter",
		subtitle: "OpenRouter",
		iconKey: "openrouter",
		baseUrl: "https://openrouter.ai/api/v1",
		hint: "One key for many providers' models. Use a key from openrouter.ai.",
	},
	{
		id: "requesty",
		name: "Requesty",
		subtitle: "Requesty",
		iconKey: "requesty",
		baseUrl: "https://router.requesty.ai/v1",
		hint: "Requesty's router. Use a key from requesty.ai.",
	},
	CUSTOM_PRESET,
];

/** Comparable form of a base URL: no trailing slashes, lower-case scheme and host. */
function normalised(url: string): string {
	const trimmed = url.trim().replace(/\/+$/, "");
	try {
		const parsed = new URL(trimmed);
		return `${parsed.protocol}//${parsed.host}${parsed.pathname.replace(/\/+$/, "")}`.toLowerCase();
	} catch {
		return trimmed.toLowerCase();
	}
}

/**
 * Which preset a saved connection was made from, read from its base URL, so
 * its card shows "Claude" rather than "OpenAI-Compatible". Any other address
 * is the custom kind. null for a connection of a provider the server no longer
 * supports (one that could only have been made before this change).
 */
export function presetForConnection(
	provider: string,
	baseUrl: string | null | undefined
): ConnectionPreset | null {
	if (provider !== OWN_PROVIDER) return null;
	const address = normalised(baseUrl ?? "");
	return (
		CONNECTION_PRESETS.find((preset) => preset.baseUrl && normalised(preset.baseUrl) === address) ??
		CUSTOM_PRESET
	);
}

/** What the connect form has typed. */
export interface ConnectionDraft {
	base_url: string | null;
	api_key: string | null;
	/** Extra headers; absent for none. */
	headers?: Record<string, string>;
}
