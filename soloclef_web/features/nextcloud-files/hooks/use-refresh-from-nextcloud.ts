"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { nextcloudFilesApi } from "../api/nextcloud-files-api";
import { type RefreshState, refreshFailure, refreshOutcome } from "../lib/refresh-state";

// A reply faster than this never shows "Refreshing…", so a quick check does not
// flicker the label through a state nobody could read.
const PENDING_DELAY_MS = 250;

export function useRefreshFromNextcloud(connectorId: number, documentId: number) {
	const [state, setState] = useState<RefreshState>({ phase: "idle" });
	const running = useRef(false);
	const pendingTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

	useEffect(
		() => () => {
			if (pendingTimer.current) clearTimeout(pendingTimer.current);
		},
		[]
	);

	const refresh = useCallback(async () => {
		if (running.current) return;
		running.current = true;
		pendingTimer.current = setTimeout(() => setState({ phase: "pending" }), PENDING_DELAY_MS);
		let next: RefreshState;
		try {
			next = refreshOutcome((await nextcloudFilesApi.refresh(connectorId, documentId)).status);
		} catch (error) {
			next = refreshFailure(error);
		}
		if (pendingTimer.current) clearTimeout(pendingTimer.current);
		pendingTimer.current = null;
		running.current = false;
		setState(next);
	}, [connectorId, documentId]);

	return { state, refresh };
}
