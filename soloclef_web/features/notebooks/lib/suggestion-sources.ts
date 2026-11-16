import type { NotebookSource } from "../model/notebook";

/**
 * What the suggested ideas of a notebook depend on: its ticked sources and
 * where each is in being read.
 *
 * The server writes ideas only from sources that have been read, and the
 * ideas were cached here for ten minutes under a key that named no sources.
 * Asked while an upload was being read, the empty chat kept "no questions"
 * after it finished; a source ticked, read again or added kept the old ones.
 * The signature goes in the ideas' query key, so any such change asks again,
 * and `settled` holds the ask until no ticked source is still being read, so
 * a set of uploads is asked about once, when all of it can be.
 */
export interface SuggestionSources {
	signature: string;
	settled: boolean;
}

export function suggestionSources(sources: readonly NotebookSource[]): SuggestionSources {
	const ticked = sources
		.filter((source) => source.enabled)
		.map((source) => `${source.kind}:${source.target_id}:${source.status ?? ""}`)
		.sort();
	const settled = !sources.some(
		(source) => source.enabled && (source.status === "pending" || source.status === "processing")
	);
	return { signature: ticked.join(","), settled };
}
