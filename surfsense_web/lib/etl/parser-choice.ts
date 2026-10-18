import type {
	DocumentParser,
	EtlCapabilities,
	ParserCapability,
} from "@/contracts/types/document.types";

/** What the upload dialog offers for one parser. */
export interface ParserOption {
	parser: DocumentParser;
	available: boolean;
	/** Stable backend token ("missing_api_key", ...) when unavailable. */
	unavailableReason: string | null;
	/** Page-credit multiplier to warn about, or null when nothing is charged. */
	costMultiplier: number | null;
}

export interface ParserChoice {
	options: ParserOption[];
	/** The parser to send with the upload; undefined lets the server default apply. */
	effectiveParser: DocumentParser | undefined;
}

function toOption(capability: ParserCapability, billingEnabled: boolean): ParserOption {
	return {
		parser: capability.parser,
		available: capability.available,
		unavailableReason: capability.available ? null : capability.unavailable_reason,
		costMultiplier:
			billingEnabled && capability.page_multiplier > 0 ? capability.page_multiplier : null,
	};
}

/**
 * The Local/Cloud choice given what the backend reports.
 *
 * Fails closed: until capabilities arrive nothing is offered and nothing is
 * sent, so the server default applies. A selection that cannot run here
 * falls back to the server default, or to any parser that can run.
 */
export function resolveParserChoice(
	capabilities: EtlCapabilities | undefined,
	selected: DocumentParser | undefined
): ParserChoice {
	if (!capabilities) return { options: [], effectiveParser: undefined };

	const options = capabilities.parsers.map((capability) =>
		toOption(capability, capabilities.page_credit_billing_enabled)
	);
	const runnable = (parser: DocumentParser | undefined) =>
		options.some((option) => option.parser === parser && option.available);

	const effectiveParser = runnable(selected)
		? selected
		: runnable(capabilities.default_parser)
			? capabilities.default_parser
			: options.find((option) => option.available)?.parser;

	return { options, effectiveParser };
}

/**
 * Every extension an upload can carry to ``parser``: the parser's own
 * document types, the parser-agnostic ones, and images when the vision LLM
 * reads them. Lower-case, with the leading dot.
 */
export function acceptedExtensions(
	capabilities: EtlCapabilities,
	parser: DocumentParser | undefined,
	options: { visionRequested: boolean }
): Set<string> {
	const chosen = capabilities.parsers.find(
		(capability) => capability.parser === (parser ?? capabilities.default_parser)
	);
	const { plaintext, direct_convert, audio, image } = capabilities.always_handled;
	return new Set(
		[
			...(chosen?.available ? chosen.document_extensions : []),
			...plaintext,
			...direct_convert,
			...audio,
			...(options.visionRequested ? image : []),
		].map((ext) => ext.toLowerCase())
	);
}

export function fileExtension(name: string): string {
	const dot = name.lastIndexOf(".");
	return dot > 0 ? name.slice(dot).toLowerCase() : "";
}

/** Split files into those ``accepted`` covers and those it does not. */
export function partitionByExtension<T extends { name: string }>(
	files: T[],
	accepted: Set<string>
): { accepted: T[]; rejected: T[] } {
	const result = { accepted: [] as T[], rejected: [] as T[] };
	for (const file of files) {
		(accepted.has(fileExtension(file.name)) ? result.accepted : result.rejected).push(file);
	}
	return result;
}
