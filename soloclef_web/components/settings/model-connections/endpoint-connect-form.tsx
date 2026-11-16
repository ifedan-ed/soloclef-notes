import { useEffect, useMemo, useState } from "react";
import { ApiBaseUrlField, ApiKeyField, HeadersField } from "./connect-fields";
import { type HeaderRow, headersFromRows } from "./connection-headers";
import type { ConnectionDraft, ConnectionPreset } from "./connection-presets";

interface EndpointConnectFormProps {
	preset: ConnectionPreset;
	onDraftChange: (draft: ConnectionDraft, canSubmit: boolean) => void;
}

/**
 * The one connect form: an OpenAI-compatible endpoint's base URL, its key and
 * any extra headers it wants. A preset only starts the base URL off typed.
 *
 * The server calls the endpoint itself and requires a key and an address
 * (soloclef_api/internal/modelconnections), so both are required here too:
 * a form that let either through would only be refused one step later.
 */
export function EndpointConnectForm({ preset, onDraftChange }: EndpointConnectFormProps) {
	const [baseUrl, setBaseUrl] = useState(preset.baseUrl);
	const [apiKey, setApiKey] = useState("");
	const [headerRows, setHeaderRows] = useState<HeaderRow[]>([]);
	const baseUrlValue = baseUrl.trim();
	const apiKeyValue = apiKey.trim();
	const headersRead = useMemo(() => headersFromRows(headerRows), [headerRows]);
	const headers =
		headersRead.problem === null && Object.keys(headersRead.headers).length > 0
			? headersRead.headers
			: undefined;
	// Rows that do not read as headers are said under them and block
	// connecting, rather than being sent as something else.
	const canSubmit = Boolean(baseUrlValue) && Boolean(apiKeyValue) && headersRead.problem === null;

	useEffect(() => {
		onDraftChange(
			{ base_url: baseUrlValue || null, api_key: apiKeyValue || null, headers },
			canSubmit
		);
	}, [apiKeyValue, baseUrlValue, canSubmit, headers, onDraftChange]);

	return (
		<div className="flex flex-col gap-4">
			<ApiBaseUrlField
				value={baseUrl}
				onChange={setBaseUrl}
				placeholder={preset.baseUrl || undefined}
				hint={preset.hint}
			/>
			<ApiKeyField
				value={apiKey}
				onChange={setApiKey}
				label="API key"
				placeholder="Enter your API key"
			/>
			<HeadersField
				rows={headerRows}
				onChange={setHeaderRows}
				problem={headersRead.problem}
				hint="Only if your endpoint asks for more than the key, such as an organisation header. Values are stored like the key and never shown again."
			/>
		</div>
	);
}
