"use client";

import { useCallback, useEffect, useState } from "react";

export type HomeView = "grid" | "list";
const KEY = "surfsense:notebooks-home-view";

/** Grid or list on the notebooks home; remembered per browser, best effort. */
export function useHomeView() {
	const [view, setView] = useState<HomeView>("grid");
	useEffect(() => {
		try {
			if (window.localStorage.getItem(KEY) === "list") setView("list");
		} catch {
			// Storage is a convenience; the grid is the default.
		}
	}, []);
	const update = useCallback((next: HomeView) => {
		setView(next);
		try {
			window.localStorage.setItem(KEY, next);
		} catch {
			// Storage is a convenience; the choice holds for this visit.
		}
	}, []);
	return [view, update] as const;
}
