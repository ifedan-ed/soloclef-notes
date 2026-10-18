"use client";

import { useAtomValue } from "jotai";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useRef, useState } from "react";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import {
	justFinished,
	type NotifyPreference,
	notifyPreferenceKey,
	parseNotifyPreference,
	parseStarted,
	rememberStarted,
	STARTED_OUTPUTS_KEY,
	shouldOfferNotifications,
} from "../lib/studio-notify";
import type { StudioOutput } from "../model/notebook";

function read(key: string): string | null {
	try {
		return window.localStorage.getItem(key);
	} catch {
		return null;
	}
}

function write(key: string, value: string) {
	try {
		window.localStorage.setItem(key, value);
	} catch {
		// Storage is a convenience; notifications simply stay off without it.
	}
}

/**
 * "Get notified when it's ready": an opt-in, remembered per user in this
 * browser, and a browser notification when an output the user started here
 * finishes while the tab is hidden.
 */
export function useStudioNotifications(outputs: readonly StudioOutput[] | undefined) {
	const t = useTranslations("notebooks");
	const userId = useAtomValue(currentUserAtom).data?.id;
	const [preference, setPreference] = useState<NotifyPreference>(null);
	const [supported, setSupported] = useState(false);
	const [permission, setPermission] = useState<string | null>(null);
	const previous = useRef<Map<number, string> | null>(null);

	useEffect(() => {
		const available = typeof window !== "undefined" && "Notification" in window;
		setSupported(available);
		setPermission(available ? Notification.permission : null);
		if (userId) setPreference(parseNotifyPreference(read(notifyPreferenceKey(String(userId)))));
	}, [userId]);

	const choose = useCallback(
		async (enable: boolean) => {
			if (!userId) return;
			let next: NotifyPreference = "off";
			if (enable && "Notification" in window) {
				const result = await Notification.requestPermission();
				setPermission(result);
				next = result === "granted" ? "on" : "off";
			}
			write(notifyPreferenceKey(String(userId)), next);
			setPreference(next);
		},
		[userId]
	);

	const markStarted = useCallback((outputId: number) => {
		write(
			STARTED_OUTPUTS_KEY,
			JSON.stringify(rememberStarted(parseStarted(read(STARTED_OUTPUTS_KEY)), outputId))
		);
	}, []);

	useEffect(() => {
		if (!outputs) return;
		const statuses = new Map(outputs.map((output) => [output.id, output.status as string]));
		const before = previous.current;
		previous.current = statuses;
		if (!before || preference !== "on" || !document.hidden) return;
		if (!("Notification" in window) || Notification.permission !== "granted") return;
		for (const output of justFinished(before, outputs, parseStarted(read(STARTED_OUTPUTS_KEY)))) {
			const notification = new Notification(
				output.status === "ready" ? t("notify.ready_title") : t("notify.failed_title"),
				{ body: output.title, tag: `studio-output-${output.id}` }
			);
			notification.onclick = () => {
				window.focus();
				notification.close();
			};
		}
	}, [outputs, preference, t]);

	return {
		offer: shouldOfferNotifications(supported, permission, preference),
		enable: () => void choose(true),
		dismiss: () => void choose(false),
		markStarted,
	};
}
