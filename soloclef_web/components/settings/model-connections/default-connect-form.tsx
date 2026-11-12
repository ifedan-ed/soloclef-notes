import { useEffect, useMemo, useState } from "react";
import { ApiBaseUrlField, ApiKeyField, HeadersField } from "./connect-fields";
import { type HeaderRow, headersFromRows } from "./connection-headers";
import type { ProviderConnectFormProps } from "./provider-metadata";

// An OpenAI-compatible endpoint is not here: the server calls it directly
// with the account's own key, and requires one.
const OPTIONAL_API_KEY_PROVIDERS = new Set(["ollama_chat", "lm_studio"]);

// The one provider whose requests the server sends itself, so the one whose
// extra headers it can send.
const TAKES_HEADERS = "openai_compatible";

function baseUrlHint(provider: string) {
	if (provider === "ollama_chat" || provider === "lm_studio") {
		return "For local servers, use host.docker.internal instead of localhost.";
	}
	if (provider === "openai_compatible") {
		return "Enter the exact API base URL, used as-is. Most endpoints end in /v1 (e.g. https://api.example.com/v1). It must be the endpoint's public address.";
	}
	if (
		provider === "openai" ||
		provider === "anthropic" ||
		provider === "openrouter" ||
		provider === "requesty"
	) {
		return "Override only if you route through a proxy or gateway.";
	}
	return undefined;
}

/**
 * Connect form for OpenAI-compatible / native key providers (OpenAI, Anthropic,
 * OpenRouter, OpenAI-Compatible, LM Studio, Ollama, …). The base URL is
 * prefilled from the provider default.
 */
export function DefaultConnectForm({
	provider,
	defaultBaseUrl,
	baseUrlRequired,
	onDraftChange,
}: ProviderConnectFormProps) {
	const [baseUrl, setBaseUrl] = useState(defaultBaseUrl);
	const [apiKey, setApiKey] = useState("");
	const [headerRows, setHeaderRows] = useState<HeaderRow[]>([]);
	const isApiKeyOptional = OPTIONAL_API_KEY_PROVIDERS.has(provider);
	const takesHeaders = provider === TAKES_HEADERS;
	const hint = baseUrlHint(provider);
	const apiKeyValue = apiKey.trim();
	const headersRead = useMemo(() => headersFromRows(headerRows), [headerRows]);
	const headers =
		takesHeaders && headersRead.problem === null && Object.keys(headersRead.headers).length > 0
			? headersRead.headers
			: undefined;
	const canSubmit =
		!(baseUrlRequired && !baseUrl.trim()) &&
		(isApiKeyOptional || Boolean(apiKeyValue)) &&
		// Rows that do not read as headers are said under them and block
		// connecting, rather than being sent as something else.
		(!takesHeaders || headersRead.problem === null);

	useEffect(() => {
		onDraftChange(
			{ base_url: baseUrl || null, api_key: apiKeyValue || null, extra: {}, headers },
			canSubmit
		);
	}, [apiKeyValue, baseUrl, canSubmit, headers, onDraftChange]);

	return (
		<div className="flex flex-col gap-4">
			<ApiBaseUrlField
				value={baseUrl}
				onChange={setBaseUrl}
				placeholder={defaultBaseUrl}
				hint={hint}
			/>
			<ApiKeyField
				value={apiKey}
				onChange={setApiKey}
				label={isApiKeyOptional ? "API Key (optional)" : "API Key"}
				placeholder="Enter your API key"
			/>
			{takesHeaders ? (
				<HeadersField
					rows={headerRows}
					onChange={setHeaderRows}
					problem={headersRead.problem}
					hint="Only if your endpoint asks for more than the key, such as an organisation header. Values are stored like the key and never shown again."
				/>
			) : null}
		</div>
	);
}
