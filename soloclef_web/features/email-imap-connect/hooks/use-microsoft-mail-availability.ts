"use client";

import { useEffect, useState } from "react";
import { microsoftMailApi } from "../api/microsoft-mail-api";

/**
 * Whether this server can connect Microsoft mailboxes (the administrator set
 * up the Microsoft app). `undefined` while asking; `false` if it cannot say.
 */
export function useMicrosoftMailAvailability(workspaceId: number | null) {
	const [configured, setConfigured] = useState<boolean | undefined>(undefined);

	useEffect(() => {
		if (!workspaceId) return;
		let current = true;
		microsoftMailApi
			.availability(workspaceId)
			.then((result) => {
				if (current) setConfigured(result.configured);
			})
			.catch(() => {
				if (current) setConfigured(false);
			});
		return () => {
			current = false;
		};
	}, [workspaceId]);

	return configured;
}
