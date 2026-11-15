// Who names the sources a copy lists. A signed-in page asks the API for its
// passages' documents; a public notebook page already holds its sources'
// titles and must ask nothing with a session.

import { decodeSourceMarker } from "@/lib/citations/addressless-source";
import type { CitationNumbering } from "@/lib/citations/citation-numbering";
import { type CitedSource, documentSourceName, UNNAMED_SOURCE } from "./cited-source";
import { type CitedPassage, fetchCitedPassages } from "./cited-sources-api";

/** A source's name, and its address when it can be opened outside the app. */
export interface SourceName {
	name: string;
	href?: string;
}

/** Names the numbered sources of one answer, by their numbering keys. */
export type SourceNamer = (keys: readonly string[]) => Promise<ReadonlyMap<string, SourceName>>;

/** The numbered sources of `numbering`, in number order, named by `namer`. */
export async function namedSources(
	numbering: CitationNumbering,
	namer: SourceNamer
): Promise<CitedSource[]> {
	if (numbering.order.length === 0) return [];
	let names: ReadonlyMap<string, SourceName>;
	try {
		names = await namer(numbering.order);
	} catch {
		// A copy that cannot name its sources still copies: the numbers
		// stay and the list says the titles could not be read.
		names = new Map();
	}
	return numbering.order.map((key, i) => ({
		number: i + 1,
		...(names.get(key) ?? { name: UNNAMED_SOURCE }),
	}));
}

/** A web citation names itself: its address. */
export function urlSourceName(key: string): SourceName | null {
	if (!key.startsWith("url:")) return null;
	const url = key.slice("url:".length);
	return { name: url, href: url };
}

/**
 * An email or a connected app's result names itself too: the label its
 * marker carries (lib/citations/addressless-source.ts). It has no href,
 * because there is nothing outside the app to open.
 */
export function addresslessSourceName(key: string): SourceName | null {
	if (!key.startsWith("source:")) return null;
	const source = decodeSourceMarker(key.slice("source:".length));
	if (!source) return null;
	return { name: `${source.source === "mail" ? "Email" : "Connected app"}: ${source.label}` };
}

/** The chunk id a passage's key names, or null for another kind of key. */
export function chunkIdOfKey(key: string): number | null {
	if (!key.startsWith("chunk:")) return null;
	const id = Number(key.slice("chunk:".length));
	return Number.isSafeInteger(id) ? id : null;
}

// Passage titles already read this session: a second copy of the same
// answer, or a copy right after the prefetch, asks nothing. A failed read is
// dropped, so the next copy tries again.
const passageCache = new Map<number, Promise<CitedPassage | null>>();

/** The signed-in namer: passages through the API (cached), addresses as themselves. */
export const accountSourceNamer: SourceNamer = async (keys) => {
	const names = new Map<string, SourceName>();
	const missing = keys
		.map(chunkIdOfKey)
		.filter((id): id is number => id !== null && !passageCache.has(id));
	if (missing.length > 0) {
		const request = fetchCitedPassages(missing);
		for (const id of missing) {
			const one = request.then((found) => found.find((p) => p.chunk_id === id) ?? null);
			one.catch(() => passageCache.delete(id));
			passageCache.set(id, one);
		}
	}
	await Promise.all(
		keys.map(async (key) => {
			const named = urlSourceName(key) ?? addresslessSourceName(key);
			if (named) {
				names.set(key, named);
				return;
			}
			const id = chunkIdOfKey(key);
			const passage = id === null ? null : await (passageCache.get(id) ?? null)?.catch(() => null);
			if (passage) names.set(key, { name: documentSourceName(passage.title, passage.page) });
		})
	);
	return names;
};
