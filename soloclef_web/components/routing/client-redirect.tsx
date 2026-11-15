"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

/**
 * A page that only sends the browser on, for an address the static site keeps
 * as a file. Caddy answers a request for each such address with the redirect
 * itself (docker/proxy-go/routes.caddy, "Pages that are only a redirect"), so
 * this runs only for a link followed inside the app, which never asks the
 * server. replace: Back must not return to a page that would only move on again.
 */
export function ClientRedirect({ to }: { to: string }) {
	const router = useRouter();
	useEffect(() => {
		router.replace(to);
	}, [router, to]);
	return null;
}
