"use client";

import { useCallback, useEffect, useState } from "react";

const PREFIX = "surfsense:notebook-panel-collapsed:";

/** Whether a notebook side panel is collapsed; remembered per browser, best effort. */
export function usePanelPreference(panel: "sources" | "studio") {
	const [collapsed, setCollapsed] = useState(false);
	// Read after mount so server and client render the same first frame.
	useEffect(() => {
		try {
			if (window.localStorage.getItem(PREFIX + panel) === "1") setCollapsed(true);
		} catch {
			// Storage is a convenience; the panel works without it.
		}
	}, [panel]);
	const update = useCallback(
		(next: boolean) => {
			setCollapsed(next);
			try {
				window.localStorage.setItem(PREFIX + panel, next ? "1" : "0");
			} catch {
				// Storage is a convenience; the panel works without it.
			}
		},
		[panel]
	);
	return [collapsed, update] as const;
}
