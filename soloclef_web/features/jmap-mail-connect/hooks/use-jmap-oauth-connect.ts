"use client";

import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { AppError } from "@/lib/error";
import { jmapMailApi } from "../api/jmap-mail-api";
import { returnPathWithoutJmapOutcome } from "../lib/jmap-mail-outcome";

/**
 * Sign in to a Stalwart server on its own page: the API checks the server
 * first (that it is Stalwart and accepts this app's sign-in), then the
 * browser leaves for it and comes back with `?jmap_mail=<outcome>`.
 */
export function useJmapOAuthConnect() {
	const t = useTranslations("jmap_mail_connect");
	const [starting, setStarting] = useState(false);

	const start = useCallback(
		async (serverUrl: string) => {
			setStarting(true);
			try {
				const { auth_url } = await jmapMailApi.oauthConnect(
					serverUrl,
					returnPathWithoutJmapOutcome(window.location)
				);
				// The browser leaves; `starting` stays true until it has gone.
				window.location.assign(auth_url);
			} catch (error) {
				// A 400 carries the API's own sentence about this mail server
				// (not Stalwart, refuses this app's sign-in), which is the useful
				// thing to show; a fault has nothing worth showing.
				const appError = error instanceof AppError ? error : null;
				if (appError?.code === "jmap_mail_oauth_not_configured") {
					toast.error(t("oauth_not_configured"));
				} else if (appError?.status === 400 && appError.message) {
					toast.error(t("oauth_start_refused", { reason: appError.message }));
				} else {
					toast.error(t("oauth_start_failed"));
				}
				setStarting(false);
			}
		},
		[t]
	);

	return { start, starting };
}
