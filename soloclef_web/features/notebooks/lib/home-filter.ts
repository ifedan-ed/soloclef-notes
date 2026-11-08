import type { Notebook } from "../model/notebook";

export type HomeTab = "all" | "recent";
export const RECENT_LIMIT = 12;

function matches(notebook: Notebook, query: string): boolean {
	const needle = query.trim().toLocaleLowerCase();
	return !needle || notebook.title.toLocaleLowerCase().includes(needle);
}

function byUpdated(a: Notebook, b: Notebook): number {
	return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime() || b.id - a.id;
}

/**
 * What the home shows. "All": the user's featured notebooks, then every
 * notebook newest first. "Recent": only the most recently used, no featured
 * row. A search narrows both by title.
 */
export function homeSections(
	notebooks: readonly Notebook[],
	tab: HomeTab,
	query: string
): { featured: Notebook[]; notebooks: Notebook[] } {
	const visible = notebooks.filter((notebook) => matches(notebook, query)).sort(byUpdated);
	if (tab === "recent") return { featured: [], notebooks: visible.slice(0, RECENT_LIMIT) };
	return { featured: visible.filter((notebook) => notebook.featured), notebooks: visible };
}
