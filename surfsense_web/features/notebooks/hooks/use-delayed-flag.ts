"use client";

import { useEffect, useState } from "react";

/**
 * True only once ``active`` has stayed true for ``delayMs``. Loading states
 * that resolve quickly never paint a spinner, so the page does not flash.
 */
export function useDelayedFlag(active: boolean, delayMs = 250): boolean {
	const [shown, setShown] = useState(false);
	useEffect(() => {
		if (!active) {
			setShown(false);
			return;
		}
		const timer = window.setTimeout(() => setShown(true), delayMs);
		return () => window.clearTimeout(timer);
	}, [active, delayMs]);
	return shown;
}
