"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { type NextcloudConnection, nextcloudConnectApi } from "../api/nextcloud-connect-api";
import {
	afterError,
	IDLE,
	isPastExpiry,
	type LoginFlowState,
	pollDelay,
	waitingFor,
} from "../lib/login-flow";

/**
 * One "Connect Nextcloud" attempt: start a login on the backend, let the
 * person open the login page, and poll the backend (which polls Nextcloud)
 * until it reports the connection saved. An abandoned attempt is cancelled.
 */
export function useNextcloudLogin(
	workspaceId: number,
	onConnected: (connection: NextcloudConnection | null) => void
) {
	const [state, setState] = useState<LoginFlowState>(IDLE);
	const connected = useRef(onConnected);
	connected.current = onConnected;
	const waitingFlow = useRef<string | null>(null);

	const start = useCallback(async () => {
		setState({ phase: "starting" });
		try {
			const started = await nextcloudConnectApi.start(workspaceId);
			waitingFlow.current = started.flow_id;
			setState(waitingFor(started, Date.now()));
		} catch (error) {
			setState(afterError(error));
		}
	}, [workspaceId]);

	const cancel = useCallback(() => {
		const flowId = waitingFlow.current;
		waitingFlow.current = null;
		setState(IDLE);
		if (flowId) void nextcloudConnectApi.cancel(workspaceId, flowId).catch(() => undefined);
	}, [workspaceId]);

	const flowId = state.phase === "waiting" ? state.flowId : null;
	const expiresAt = state.phase === "waiting" ? state.expiresAt : 0;
	useEffect(() => {
		if (!flowId) return;
		let stopped = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const begun = Date.now();
		const tick = async () => {
			if (stopped) return;
			if (isPastExpiry(Date.now(), expiresAt)) {
				waitingFlow.current = null;
				setState({ phase: "expired" });
				return;
			}
			try {
				const progress = await nextcloudConnectApi.poll(workspaceId, flowId);
				if (stopped) return;
				if (progress.status === "connected") {
					waitingFlow.current = null;
					setState({ phase: "connected" });
					connected.current(progress.connection);
					return;
				}
			} catch (error) {
				if (stopped) return;
				waitingFlow.current = null;
				setState(afterError(error));
				return;
			}
			timer = setTimeout(tick, pollDelay(Date.now() - begun));
		};
		timer = setTimeout(tick, pollDelay(0));
		return () => {
			stopped = true;
			if (timer) clearTimeout(timer);
		};
	}, [flowId, expiresAt, workspaceId]);

	// Leaving the page mid-attempt: tell the backend to stop keeping it.
	useEffect(
		() => () => {
			const pending = waitingFlow.current;
			if (pending) void nextcloudConnectApi.cancel(workspaceId, pending).catch(() => undefined);
		},
		[workspaceId]
	);

	return { state, start, cancel, reset: () => setState(IDLE) };
}
