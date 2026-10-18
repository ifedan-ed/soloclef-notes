import type { NotebookSource } from "../model/notebook";

export type SelectAllState = "all" | "none" | "some";

export function selectAllState(sources: Pick<NotebookSource, "enabled">[]): SelectAllState {
	if (sources.length === 0) return "none";
	const enabled = sources.filter((source) => source.enabled).length;
	if (enabled === 0) return "none";
	return enabled === sources.length ? "all" : "some";
}

/** Clicking "Select all" selects everything unless everything is already selected. */
export function nextSelectAllValue(state: SelectAllState): boolean {
	return state !== "all";
}

export function enabledCount(sources: Pick<NotebookSource, "enabled">[]): number {
	return sources.filter((source) => source.enabled).length;
}

/** Apply a toggle optimistically so the checkbox never waits on the network. */
export function withToggled(
	sources: NotebookSource[],
	sourceId: number | "all",
	enabled: boolean
): NotebookSource[] {
	return sources.map((source) =>
		sourceId === "all" || source.id === sourceId ? { ...source, enabled } : source
	);
}
