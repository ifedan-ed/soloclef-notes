import {
	DEFAULT_RETRIEVAL_SCOPE,
	isRetrievalScope,
	type RetrievalScope,
} from "@/contracts/types/retrieval-scope.types";

// v2: the choice is the account's, kept on one cookie for the whole dashboard.
// v1 was written once per workspace on that workspace's own path; a browser
// sends the cookie with the longer path first, so reusing the name would let
// a stale v1 cookie hide every new choice for the year it lives.
export const RETRIEVAL_SCOPE_COOKIE = "surfsense_retrieval_scope_v2";
export const RETRIEVAL_SCOPE_COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

export function parseRetrievalScopeCookie(
	value: string | undefined,
	expectedUserId?: string
): RetrievalScope {
	if (!value) return DEFAULT_RETRIEVAL_SCOPE;
	try {
		const [userId, scope] = decodeURIComponent(value).split(":");
		if (expectedUserId && userId !== expectedUserId) return DEFAULT_RETRIEVAL_SCOPE;
		return isRetrievalScope(scope) ? scope : DEFAULT_RETRIEVAL_SCOPE;
	} catch {
		return DEFAULT_RETRIEVAL_SCOPE;
	}
}

export function persistRetrievalScopeCookie(
	userId: string | undefined,
	scope: RetrievalScope
): void {
	if (typeof document === "undefined" || !userId) return;
	try {
		const value = encodeURIComponent(`${userId}:${scope}`);
		// biome-ignore lint/suspicious/noDocumentCookie: Cookie Store is not consistently available.
		document.cookie = `${RETRIEVAL_SCOPE_COOKIE}=${value}; path=/dashboard; max-age=${RETRIEVAL_SCOPE_COOKIE_MAX_AGE}; samesite=lax`;
	} catch {
		// The in-memory preference still works when browser cookies are disabled.
	}
}
