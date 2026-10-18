"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useRef } from "react";
import { toast } from "sonner";

/**
 * Back from Google's consent page (``?google_drive=connected`` or an error
 * code): say how it went once and drop the marker from the URL.
 */
export function useGoogleDriveReturn(onConnected?: () => void) {
	const t = useTranslations("google_drive_connect");
	const params = useSearchParams();
	const router = useRouter();
	const pathname = usePathname();
	const outcome = params.get("google_drive");
	const handled = useRef<string | null>(null);

	useEffect(() => {
		if (!outcome || handled.current === outcome) return;
		handled.current = outcome;
		if (outcome === "connected") {
			toast.success(t("connected_toast"));
			onConnected?.();
		} else if (outcome === "google_drive_scope_missing") {
			toast.error(t("scope_missing"));
		} else if (outcome !== "google_drive_oauth_denied") {
			toast.error(t("connect_failed"));
		}
		const next = new URLSearchParams(params.toString());
		next.delete("google_drive");
		const query = next.toString();
		router.replace(`${pathname}${query ? `?${query}` : ""}`, { scroll: false });
	}, [outcome, params, pathname, router, t, onConnected]);
}
