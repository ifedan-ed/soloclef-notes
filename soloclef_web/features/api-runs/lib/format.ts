/** Cost and duration as the runs table shows them. */

/** Dollars to four places; ``freeLabel`` for a run that cost nothing; a dash when unknown. */
export function formatCost(costMicros: number | null | undefined, freeLabel: string): string {
	if (costMicros == null) return "—";
	if (costMicros === 0) return freeLabel;
	return `$${(costMicros / 1_000_000).toFixed(4)}`;
}

export function formatDuration(ms: number | null | undefined): string {
	if (ms == null) return "—";
	if (ms < 1000) return `${ms}ms`;
	return `${(ms / 1000).toFixed(1)}s`;
}
