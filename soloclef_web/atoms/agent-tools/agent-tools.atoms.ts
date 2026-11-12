import { atom, type Setter } from "jotai";
import { atomWithQuery } from "jotai-tanstack-query";
import { agentToolsApiService } from "@/lib/apis/agent-tools-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

export const agentToolsAtom = atomWithQuery((_get) => ({
	queryKey: cacheKeys.agentTools.all(),
	staleTime: 30 * 60 * 1000, // 30 min – tool list rarely changes
	queryFn: async () => agentToolsApiService.getTools(),
}));

// One list per browser: the tools are the account's, and an account has one
// chat, so there is nothing further to key the choice by.
const STORAGE_KEY = "surfsense-disabled-tools";

function loadDisabledTools(): string[] {
	if (typeof window === "undefined") return [];
	try {
		const raw = localStorage.getItem(STORAGE_KEY);
		return raw ? (JSON.parse(raw) as string[]) : [];
	} catch {
		return [];
	}
}

function saveDisabledTools(tools: string[]) {
	if (typeof window === "undefined") return;
	// An empty list is stored as no entry, so "nothing disabled" and "never
	// chosen" read back the same.
	if (tools.length === 0) {
		localStorage.removeItem(STORAGE_KEY);
	} else {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(tools));
	}
}

const disabledToolsBaseAtom = atom<string[]>([]);

/**
 * Whether the base atom holds the stored list yet. It starts false because
 * the server render has no localStorage: the first read in the browser goes
 * to storage, and every write after that keeps the atom and storage equal.
 */
const hydratedAtom = atom(false);

/** Write the list to the atom and to storage as one step. */
function storeDisabledTools(set: Setter, tools: string[]) {
	set(disabledToolsBaseAtom, tools);
	set(hydratedAtom, true);
	saveDisabledTools(tools);
}

/**
 * Read/write atom for the set of disabled tool names, kept in localStorage.
 *
 * The read falls through to storage until hydration, so a turn sent before
 * the composer's effect has run still carries the person's choice.
 */
export const disabledToolsAtom = atom(
	(get) => (get(hydratedAtom) ? get(disabledToolsBaseAtom) : loadDisabledTools()),
	(get, set, update: string[] | ((prev: string[]) => string[])) => {
		// Through the public atom, so a write before hydration builds on the
		// stored list and not on the empty starting value.
		const prev = get(disabledToolsAtom);
		storeDisabledTools(set, typeof update === "function" ? update(prev) : update);
	}
);

/**
 * Copy the stored list into the atom. Called once from an effect in the
 * composer, because localStorage cannot be read while rendering on the server.
 */
export const hydrateDisabledToolsAtom = atom(null, (_get, set) => {
	set(disabledToolsBaseAtom, loadDisabledTools());
	set(hydratedAtom, true);
});

/** Toggle a single tool's enabled/disabled state */
export const toggleToolAtom = atom(null, (get, set, toolName: string) => {
	const current = get(disabledToolsAtom);
	storeDisabledTools(
		set,
		current.includes(toolName) ? current.filter((t) => t !== toolName) : [...current, toolName]
	);
});

/** Derive the count of currently enabled tools */
export const enabledToolCountAtom = atom((get) => {
	const { data: tools } = get(agentToolsAtom);
	const disabled = get(disabledToolsAtom);
	if (!tools) return 0;
	return tools.length - disabled.filter((d) => tools.some((t) => t.name === d)).length;
});
