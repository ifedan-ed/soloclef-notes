import type { DocumentParser } from "@/contracts/types/document.types";

/**
 * The message keys for each parser, written out one by one. They are not
 * built from the parser's name (`parser_${id}`): a key assembled at run time
 * is one the type-checker and the locale parity test cannot see, and a
 * parser added on the server would then render as a raw dotted path.
 * All keys are in the `upload_documents` namespace.
 */
export const PARSER_NAME_KEYS = {
	local: "parser_local",
	datalab: "parser_datalab",
	llamaparse: "parser_llamaparse",
	unstructured: "parser_unstructured",
} as const satisfies Record<DocumentParser, string>;

export const PARSER_DESCRIPTION_KEYS = {
	local: "parser_local_desc",
	datalab: "parser_datalab_desc",
	llamaparse: "parser_llamaparse_desc",
	unstructured: "parser_unstructured_desc",
} as const satisfies Record<DocumentParser, string>;

/**
 * Why a parser cannot be picked, as a message key. The server sends one
 * token today, "not_configured". Any other token is shown as plainly
 * unavailable: wording it as "not configured" would state a cause the
 * server did not give.
 */
export function unavailableReasonKey(
	reason: string | null
): "parser_unavailable_not_configured" | "parser_unavailable_other" {
	return reason === "not_configured"
		? "parser_unavailable_not_configured"
		: "parser_unavailable_other";
}
