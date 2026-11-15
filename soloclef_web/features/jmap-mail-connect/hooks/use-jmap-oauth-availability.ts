"use client";

import { useEffect, useState } from "react";
import { jmapMailApi } from "../api/jmap-mail-api";

/**
 * Whether this deployment can offer Stalwart's sign-in (its client document
 * and callback are set up). `undefined` while asking; `false` when it cannot
 * say, so the app-password form is then the one way offered.
 */
export function useJmapOAuthAvailability() {
	const [configured, setConfigured] = useState<boolean | undefined>(undefined);

	useEffect(() => {
		let current = true;
		jmapMailApi
			.oauthAvailability()
			.then((result) => {
				if (current) setConfigured(result.configured);
			})
			.catch(() => {
				if (current) setConfigured(false);
			});
		return () => {
			current = false;
		};
	}, []);

	return configured;
}
