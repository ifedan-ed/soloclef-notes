import type {
	DocumentParser,
	EtlCapabilities,
	ParserCapability,
} from "@/contracts/types/document.types";

/** What a parser picker offers for one parser. */
export interface ParserOption {
	parser: DocumentParser;
	available: boolean;
	/** The server's token ("not_configured") when unavailable, else null. */
	unavailableReason: string | null;
}

export interface ParserChoice {
	/** All four parsers, in the server's order; empty until capabilities arrive. */
	options: ParserOption[];
	/**
	 * The parser to send, or undefined when there is none to send yet: the
	 * capabilities have not arrived, or the one that would apply cannot run
	 * here. A source with no parser to send must not be submitted.
	 */
	effectiveParser: DocumentParser | undefined;
}

function toOption(capability: ParserCapability): ParserOption {
	return {
		parser: capability.parser,
		available: capability.available,
		unavailableReason: capability.available ? null : capability.unavailable_reason,
	};
}

/**
 * The parser a source will be read by, given what the server reports and
 * what the person picked.
 *
 * It starts on the server's default and otherwise is exactly the pick. It
 * never moves to a different parser on its own: a pick, or a default, that
 * is not configured yields no parser at all, and the picker asks the person
 * to choose one that is. Quietly sending a file to another service than the
 * one named would be choosing where someone's document goes for them.
 */
export function resolveParserChoice(
	capabilities: EtlCapabilities | undefined,
	selected: DocumentParser | undefined
): ParserChoice {
	if (!capabilities) return { options: [], effectiveParser: undefined };

	const options = capabilities.parsers.map(toOption);
	const wanted = selected ?? capabilities.default_parser;
	const runnable = options.some((option) => option.parser === wanted && option.available);

	return { options, effectiveParser: runnable ? wanted : undefined };
}

/**
 * Every extension a source can carry to ``parser``: the parser's own
 * document types and the ones the server reads itself whatever was chosen,
 * and, with the vision switch on, the pictures the vision model describes.
 * Lower-case, with the leading dot. With no parser to send, only the
 * server-read types remain, because nothing else would be read by anything.
 */
export function acceptedExtensions(
	capabilities: EtlCapabilities,
	parser: DocumentParser | undefined,
	vision = false
): Set<string> {
	const chosen = capabilities.parsers.find((capability) => capability.parser === parser);
	const { plaintext, direct_convert } = capabilities.always_handled;
	// Pictures only when the server would take the switch: a stale "on"
	// from before the vision model went away must not admit them.
	const pictures = vision && capabilities.vision_available ? capabilities.picture_extensions : [];
	return new Set(
		[
			...(chosen?.available ? chosen.document_extensions : []),
			...plaintext,
			...direct_convert,
			...pictures,
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
