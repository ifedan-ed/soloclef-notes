/**
 * A notebook still called "Untitled notebook" names itself in the background
 * (backend: app/notebooks/autoname) from its first ready source or its first
 * question. These decide when the app should look again for the new name, so
 * it appears without a reload and without polling forever.
 */
import type { Notebook, NotebookSource } from "../model/notebook";

/** How long to look for a new name after a source becomes ready or a turn ends. */
export const TITLE_WATCH_MS = 45_000;
const WATCH_INTERVAL_MS = 3_000;
const PENDING_INTERVAL_MS = 5_000;

export interface TitleWatch {
	/** Stop looking after this time (ms since epoch). */
	until: number;
	/** The title when the watch began; a different one means the name arrived. */
	fromTitle: string;
}

type NamingFields = Pick<Notebook, "title" | "title_source" | "title_pending">;

export function readySourceCount(sources: readonly Pick<NotebookSource, "status">[] = []): number {
	return sources.filter((source) => source.status === "ready").length;
}

export function isAutoNamed(notebook: Pick<Notebook, "title_source"> | undefined): boolean {
	return notebook?.title_source === "auto";
}

/** Start watching for a new name, or null when the notebook will not rename itself. */
export function startTitleWatch(
	notebook: NamingFields | undefined,
	now: number = Date.now()
): TitleWatch | null {
	if (!notebook || !isAutoNamed(notebook)) return null;
	return { until: now + TITLE_WATCH_MS, fromTitle: notebook.title };
}

/** The notebook page's refetch interval for the notebook itself. */
export function notebookRefetchInterval(
	notebook: NamingFields | undefined,
	watch: TitleWatch | null,
	now: number = Date.now()
): number | false {
	if (!notebook || !isAutoNamed(notebook)) return false;
	if (watch && now < watch.until && notebook.title === watch.fromTitle) {
		return WATCH_INTERVAL_MS;
	}
	return notebook.title_pending ? PENDING_INTERVAL_MS : false;
}

/** The home list polls only while some notebook's name is seconds away. */
export function notebooksRefetchInterval(
	notebooks: readonly Pick<Notebook, "title_source" | "title_pending">[] | undefined
): number | false {
	return notebooks?.some((notebook) => isAutoNamed(notebook) && notebook.title_pending)
		? PENDING_INTERVAL_MS
		: false;
}

/** The list with this notebook's newer name and icon, or the same list if nothing changed. */
export function withNotebookName<T extends Pick<Notebook, "id" | "title" | "icon">>(
	list: T[] | undefined,
	notebook: Pick<Notebook, "id" | "title" | "icon" | "title_source" | "title_pending">
): T[] | undefined {
	if (!list) return list;
	let changed = false;
	const next = list.map((item) => {
		if (item.id !== notebook.id) return item;
		if (item.title === notebook.title && item.icon === notebook.icon) return item;
		changed = true;
		return {
			...item,
			title: notebook.title,
			icon: notebook.icon,
			title_source: notebook.title_source,
			title_pending: notebook.title_pending,
		};
	});
	return changed ? next : list;
}
