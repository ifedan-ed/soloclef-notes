/**
 * Notebook links once named the notebook's chat (`?chat=<id>`). A notebook has
 * one chat, so an old link's parameter is dropped rather than obeyed.
 */
const RETIRED_CHAT_PARAM = "chat";

export function hasRetiredChatParam(search: string): boolean {
	return new URLSearchParams(search).has(RETIRED_CHAT_PARAM);
}

/** ``url`` without the retired chat parameter; every other parameter stays. */
export function withoutRetiredChatParam(url: string): string {
	const next = new URL(url);
	next.searchParams.delete(RETIRED_CHAT_PARAM);
	return next.toString();
}
