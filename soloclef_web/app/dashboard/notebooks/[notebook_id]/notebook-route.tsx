"use client";

import { NotebookWorkspace } from "@/features/notebooks/ui/workspace/notebook-workspace";
import { usePathSegment } from "@/hooks/use-path-segment";
import { DYNAMIC_PREFIXES } from "@/lib/routing/dynamic-segments";

/** The notebook the address names (page.tsx says why it is read from there). */
export function NotebookRoute() {
	const segment = usePathSegment(DYNAMIC_PREFIXES.notebook);
	if (segment === null) return null;
	// Keyed: the page's file is the same for every notebook, so React would
	// otherwise keep one notebook's workspace state when moving to another.
	return <NotebookWorkspace key={segment} notebookId={Number(segment)} />;
}
