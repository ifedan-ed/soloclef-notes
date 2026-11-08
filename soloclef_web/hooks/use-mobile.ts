import * as React from "react";

const MOBILE_BREAKPOINT = 768;
const QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`;

function subscribe(onChange: () => void): () => void {
	const mql = window.matchMedia(QUERY);
	mql.addEventListener("change", onChange);
	return () => mql.removeEventListener("change", onChange);
}

const getSnapshot = () => window.innerWidth < MOBILE_BREAKPOINT;
// Server HTML (and the hydration pass that must match it) is the wide layout.
const getServerSnapshot = () => false;

/**
 * Whether the viewport is phone-sized.
 *
 * Read synchronously on the client, so a component that mounts after hydration
 * (the dashboard shell, which waits for the session) renders the phone layout
 * on its first paint instead of rendering the desktop layout and swapping. The
 * swap was a visible double paint, and it remounted the page under it: a page
 * that redirects on the server (/dashboard/<ws>, /user-settings, …) then
 * crashed the router with React error #310.
 */
export function useIsMobile() {
	return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
