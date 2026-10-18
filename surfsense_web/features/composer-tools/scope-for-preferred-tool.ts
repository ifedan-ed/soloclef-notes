import type { RetrievalScope } from "@/contracts/types/retrieval-scope.types";
import type { ComposerToolId } from "./composer-tools";

/**
 * A forced tool reaches outside the workspace, so a documents-only turn
 * widens to "all" (as the backend does). The user's saved scope is untouched.
 */
export function scopeForPreferredTool(
	scope: RetrievalScope,
	tool: ComposerToolId | null
): RetrievalScope {
	return tool && scope === "documents" ? "all" : scope;
}
