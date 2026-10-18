"use client";

import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { googleDriveApi } from "../api/google-drive-api";
import { returnPathFrom } from "../lib/picked-files";

/**
 * Connect (or reconnect): the browser goes to Google's consent page and
 * Google sends it back to this same page.
 */
export function useGoogleDriveConnect(workspaceId: number) {
	const t = useTranslations("google_drive_connect");
	const [starting, setStarting] = useState(false);
	const start = useCallback(async () => {
		setStarting(true);
		try {
			const { auth_url } = await googleDriveApi.connect(
				workspaceId,
				returnPathFrom(window.location)
			);
			window.location.assign(auth_url);
		} catch {
			toast.error(t("connect_failed"));
			setStarting(false);
		}
	}, [workspaceId, t]);
	return { start, starting };
}
