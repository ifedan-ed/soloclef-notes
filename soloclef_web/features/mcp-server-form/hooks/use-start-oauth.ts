"use client";

import { useCallback, useState } from "react";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { oauthReturnPath } from "../model/oauth-return";

/**
 * Sends the person to a saved server's OAuth sign-in. The page is left for
 * the provider's, so on success nothing more happens here; a refusal is
 * returned as the server's sentence for the caller to show.
 */
export function useStartOAuth() {
	const [starting, setStarting] = useState(false);
	const start = useCallback(async (connectorId: number): Promise<string | null> => {
		setStarting(true);
		try {
			const { authorization_url } = await connectorsApiService.startMCPOAuth(
				connectorId,
				oauthReturnPath(window.location.pathname, window.location.search)
			);
			window.location.assign(authorization_url);
			return null;
		} catch (error) {
			setStarting(false);
			return error instanceof Error && error.message
				? error.message
				: "The sign-in could not be started. Try again.";
		}
	}, []);
	return { start, starting };
}
