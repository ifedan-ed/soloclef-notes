"use client";

import { useEffect } from "react";
import { PublicHeader } from "@/components/site/public-header";
import { PublicOutputPage } from "@/features/public-sharing/ui/public-output-page";
import { usePublicShareToken } from "@/hooks/use-path-segment";

/**
 * The title Caddy wrote into this page's HTML for the link it was served for
 * (the shared item's own: scripts/share-card-template.ts), read when this
 * module first runs, which is before React hydrates the page and sets the
 * head to the file's built title. Kept with its address, so it is only ever
 * put back on the page it came with.
 */
const served =
	typeof document === "undefined"
		? null
		: { path: window.location.pathname, title: document.title };

/**
 * The shared Studio output the address names. One file serves every token
 * (lib/routing/dynamic-segments), so the token is read from the address once
 * the page is running; until then the page shows the site's bar, as the
 * link's own loading state does.
 */
export function PublicLinkRoute() {
	const token = usePublicShareToken();
	// The tab says what was shared, as the link preview does. Next.js sets
	// the built title as it finishes hydrating the head, after this page's
	// first effects, so the served title is put back whenever the head's
	// title changes away from it while this page is open.
	useEffect(() => {
		if (!token || !served || served.path !== window.location.pathname) return;
		const keep = () => {
			if (document.title !== served.title) document.title = served.title;
		};
		keep();
		const observer = new MutationObserver(keep);
		observer.observe(document.head, { subtree: true, childList: true, characterData: true });
		return () => observer.disconnect();
	}, [token]);
	if (!token) {
		return (
			<main className="min-h-screen bg-main-panel text-foreground overflow-x-hidden">
				<PublicHeader />
			</main>
		);
	}
	return <PublicOutputPage key={token} shareToken={token} />;
}
