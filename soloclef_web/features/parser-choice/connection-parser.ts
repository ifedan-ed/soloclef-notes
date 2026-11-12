import type { DocumentParser, EtlCapabilities } from "@/contracts/types/document.types";
import type { ParserChoice } from "@/lib/etl/parser-choice";

/**
 * A connection's parser as its settings show it.
 *
 * Unlike an upload, a connection has a stored choice, and the picker must
 * show exactly that: a connection whose owner has chosen nothing shows
 * nothing chosen (the server's default is not presented as if it had been
 * picked), and one whose parser has since lost its key still shows that
 * parser, marked unavailable, so the owner can see why its files stopped
 * being read and choose another.
 */
export function connectionParserChoice(
	capabilities: EtlCapabilities | undefined,
	stored: DocumentParser | null
): ParserChoice & { storedIsUnavailable: boolean } {
	if (!capabilities) return { options: [], effectiveParser: undefined, storedIsUnavailable: false };
	const options = capabilities.parsers.map((capability) => ({
		parser: capability.parser,
		available: capability.available,
		unavailableReason: capability.available ? null : capability.unavailable_reason,
	}));
	const storedOption = options.find((option) => option.parser === stored);
	return {
		options,
		effectiveParser: stored ?? undefined,
		storedIsUnavailable: storedOption !== undefined && !storedOption.available,
	};
}
