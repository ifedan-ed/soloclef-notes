/**
 * Zero as a change signal for "Your connectors".
 *
 * The REST list (GET /search-source-connectors) is the one source of the
 * rows: it has every connection of the account, with its masked config and
 * its `enabled` state, neither of which Zero syncs. Zero's rows are used
 * only to notice that something changed elsewhere (another tab, a finished
 * indexing run), at which point the panel asks for the REST list again.
 */

/** The fields of a synced row whose change means the REST list is out of date. */
export interface LiveConnectorRow {
	id: number;
	name: string;
	is_indexable: boolean;
	last_indexed_at: string | null;
	periodic_indexing_enabled: boolean;
	indexing_frequency_minutes: number | null;
	next_scheduled_at: string | null;
}

/** Zero's rows as a comparable key: ordered by id, so a reorder is no change. */
export function liveRowsKey(live: readonly LiveConnectorRow[]): string {
	return JSON.stringify(
		[...live]
			.sort((a, b) => a.id - b.id)
			.map((c) => [
				c.id,
				c.name,
				c.is_indexable,
				c.last_indexed_at,
				c.periodic_indexing_enabled,
				c.indexing_frequency_minutes,
				c.next_scheduled_at,
			])
	);
}

/**
 * Whether Zero's rows changed since the last complete result (`previousKey`,
 * null before the first one, which is the state the REST list was just
 * fetched in and so no change).
 */
export function liveRowsChanged(
	previousKey: string | null,
	live: readonly LiveConnectorRow[]
): boolean {
	return previousKey !== null && previousKey !== liveRowsKey(live);
}
