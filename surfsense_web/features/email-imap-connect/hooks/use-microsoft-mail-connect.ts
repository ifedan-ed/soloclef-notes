"use client";

import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { microsoftMailApi } from "../api/microsoft-mail-api";
import { returnPathWithoutOutcome } from "../lib/microsoft-mail-outcome";

/**
 * Connect (or, with an account id, reconnect) a Microsoft mailbox: the
 * browser goes to Microsoft's sign-in and Microsoft sends it back here.
 */
export function useMicrosoftMailConnect(workspaceId: number) {
	const t = useTranslations("email_imap_connect");
	const [starting, setStarting] = useState(false);

	const start = useCallback(
		async (accountId?: string) => {
			setStarting(true);
			try {
				const { auth_url } = await microsoftMailApi.connect(
					workspaceId,
					returnPathWithoutOutcome(window.location),
					accountId
				);
				window.location.assign(auth_url);
			} catch {
				toast.error(t("microsoft_connect_failed"));
				setStarting(false);
			}
		},
		[workspaceId, t]
	);

	return { start, starting };
}
