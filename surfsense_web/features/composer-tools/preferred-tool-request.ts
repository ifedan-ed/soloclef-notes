import type { RetrievalScope } from "@/contracts/types/retrieval-scope.types";
import type { ComposerToolId } from "./composer-tools";
import { scopeForPreferredTool } from "./scope-for-preferred-tool";

export type PreferredToolRequestFields = {
	retrieval_scope: RetrievalScope;
	preferred_tool?: ComposerToolId;
};

/** The new_chat body fields for a turn; `preferred_tool` is omitted when none was picked. */
export function preferredToolRequestFields(
	scope: RetrievalScope,
	tool: ComposerToolId | null
): PreferredToolRequestFields {
	const retrieval_scope = scopeForPreferredTool(scope, tool);
	return tool ? { retrieval_scope, preferred_tool: tool } : { retrieval_scope };
}
