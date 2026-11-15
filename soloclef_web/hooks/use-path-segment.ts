"use client";

import { usePathname } from "next/navigation";
import { useSyncExternalStore } from "react";
import { DYNAMIC_PREFIXES, idAfter, segmentAfter } from "@/lib/routing/dynamic-segments";

const noSubscription = () => () => {};

/**
 * False while React hydrates the static HTML, true after. The HTML of a
 * dynamic page was written for the placeholder, so a value read from the
 * address must not be drawn before hydration ends, or the first client
 * render would differ from the HTML it is attaching to.
 */
function useHydrated(): boolean {
	return useSyncExternalStore(
		noSubscription,
		() => true,
		() => false
	);
}

/** The segment after `prefix` in the current address (lib/routing/dynamic-segments says why). */
export function usePathSegment(prefix: string): string | null {
	const pathname = usePathname();
	return useHydrated() ? segmentAfter(pathname, prefix) : null;
}

/** The numeric id after `prefix`, or null until there is one. */
export function usePathId(prefix: string): number | null {
	const pathname = usePathname();
	return useHydrated() ? idAfter(pathname, prefix) : null;
}

/** The share token of a /public/<token> page (a shared Studio output), or null anywhere else. */
export function usePublicShareToken(): string | null {
	return usePathSegment(DYNAMIC_PREFIXES.publicLink);
}
