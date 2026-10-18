"use client";

import { useCallback, useState } from "react";
import {
	initialSourcePicks,
	parseSourcePicks,
	type SourcePicks,
} from "../lib/research-source-picks";

const KEY = "surfsense:notebook-research-sources";

function readRemembered(): SourcePicks {
	if (typeof window === "undefined") return initialSourcePicks;
	try {
		const stored = window.localStorage.getItem(KEY);
		return (stored && parseSourcePicks(JSON.parse(stored))) || initialSourcePicks;
	} catch {
		return initialSourcePicks;
	}
}

/**
 * Where the research box looks, remembered per browser so the next search
 * starts from the same sources. Read on first render (the box only mounts in
 * the browser), so the chip never repaints from Web to the remembered pick.
 */
export function useRememberedSourcePicks() {
	const [picks, setPicks] = useState<SourcePicks>(readRemembered);
	const update = useCallback((next: SourcePicks) => {
		setPicks(next);
		try {
			window.localStorage.setItem(KEY, JSON.stringify(next));
		} catch {
			// Storage is a convenience; the box works without it.
		}
	}, []);
	return [picks, update] as const;
}
