import { z } from "zod";

/**
 * The chat's tool that adds papers and pages to a notebook's sources
 * (soloclef_assistant/addsources). It is offered only in a notebook's chat.
 */
export const ADD_SOURCES_TOOL = "add_sources";

const item = z.object({
	/** added, already_a_source or not_added. */
	status: z.enum(["added", "already_a_source", "not_added"]),
	/** The source's document, for one that is a source now. */
	document_id: z.number().int().positive().optional(),
	title: z.string().optional(),
	/** Still being read or indexed: the notebook's search does not find it yet. */
	reading: z.boolean().optional(),
	/** Why a source was not added, in the tool's own words. */
	reason: z.string().optional(),
	/** The identity the assistant sent, shown for a source not added (it has no title). */
	ref: z.string().optional(),
});
export type AddSourcesItem = z.infer<typeof item>;

/**
 * What the server sends for a finished call: built from an allowlist,
 * because a card is saved with the message and published with a shared
 * thread.
 */
const card = z.object({
	notebook_id: z.number().int().positive(),
	items: z.array(item).min(1),
});
export type AddSourcesCardData = z.infer<typeof card>;

/**
 * The card for a tool result, or null for anything else: a call running or
 * waiting for approval, one that failed as a whole, or a shape from another
 * version. Null leaves the call in the turn's trace, as every other step.
 */
export function parseAddSourcesCard(result: unknown): AddSourcesCardData | null {
	const parsed = card.safeParse(result);
	return parsed.success ? parsed.data : null;
}

/** "Added 2 sources", "Added 1 source", "Added no sources": the step line's words. */
export function addSourcesSummary(data: AddSourcesCardData): string {
	const added = data.items.filter((entry) => entry.status === "added").length;
	if (added === 0) return "Added no sources";
	return added === 1 ? "Added 1 source" : `Added ${added} sources`;
}

/** The row's label: the source's title, or for one not added the identity sent. */
export function itemLabel(entry: AddSourcesItem): string {
	return entry.title?.trim() || entry.ref?.trim() || "Source";
}

/** A notebook source as the Sources panel lists it: what withLiveReading reads. */
type ListedSource = { kind: string; target_id: number; status?: string | null };

/**
 * The card with each source's "reading" taken from the notebook's sources as
 * listed now: the saved payload says how a source stood when the call ended,
 * and a source read since must not show as reading. A source the list does
 * not hold (not loaded yet, or removed since) keeps the payload's word.
 */
export function withLiveReading(
	data: AddSourcesCardData,
	sources: readonly ListedSource[] | undefined
): AddSourcesCardData {
	if (!sources) return data;
	return {
		...data,
		items: data.items.map((entry) => {
			const listed = sources.find(
				(source) => source.kind === "document" && source.target_id === entry.document_id
			);
			if (!listed || entry.status === "not_added") return entry;
			return { ...entry, reading: listed.status === "pending" || listed.status === "processing" };
		}),
	};
}
