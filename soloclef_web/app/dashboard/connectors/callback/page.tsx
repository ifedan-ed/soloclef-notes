"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useGlobalLoadingEffect } from "@/hooks/use-global-loading";
import { oauthResultCookie, oauthResultFromQuery } from "@/lib/connectors/oauth-callback";
import { settingsPath } from "@/lib/dashboard-paths";

/**
 * Where the API sends the browser once a provider's OAuth flow is done
 * (soloclef_api/internal/driveconnect and the other connects). The result
 * travels in a short-lived cookie to the connectors settings, which carry on
 * with indexing or editing; this page only hands it over and moves on.
 */
export default function ConnectorOAuthCallbackPage() {
	const router = useRouter();
	useGlobalLoadingEffect(true);

	useEffect(() => {
		// biome-ignore lint/suspicious/noDocumentCookie: Cookie Store is not consistently available.
		document.cookie = oauthResultCookie(oauthResultFromQuery(window.location.search));
		// replace: Back from the settings must not land on this hand-over again.
		router.replace(settingsPath("connectors"));
	}, [router]);

	return null;
}
