"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ServerTurnWatcher } from "./server-turn-watcher";
import { fetchServerTurnStatus, requestServerStop, type ServerTurnStatus } from "./turn-status-api";
import { onTurnStatusCheckRequested } from "./turn-status-signal";

interface UseServerTurnOptions {
	threadId: number | null;
	/** This page holds the turn's live stream (it sent the message). */
	ownsStream: boolean;
	/** Zero's "AI is responding" flag for this thread; a change triggers a check. */
	respondingHint: boolean;
	/** A turn this page watched has ended: reload the thread's messages. */
	onFinished: () => void;
}

export interface ServerTurn {
	status: ServerTurnStatus;
	/** A turn runs on this thread that this page does not stream. */
	running: boolean;
	/**
	 * The turn is stopping: Stop was pressed here and not yet answered, or the
	 * backend reports ``cancelling`` (pressed here or on another page) while
	 * it saves the partial reply. The page shows "Stopping…", not Stop.
	 */
	stopping: boolean;
	/**
	 * Stop the thread's turn. ``send`` asks the backend; by default it calls
	 * ``cancel-active-turn`` for this thread (a turn another page started).
	 */
	stop: (send?: () => Promise<void>) => Promise<void>;
}

/**
 * Whether a turn runs on the viewed thread without this page streaming it:
 * started in another tab or on another device, or before a reload. The page
 * treats such a turn as running (Stop, no Regenerate, live step list from the
 * saved progress) until the backend reports the thread idle.
 */
export function useServerTurn({
	threadId,
	ownsStream,
	respondingHint,
	onFinished,
}: UseServerTurnOptions): ServerTurn {
	const [status, setStatus] = useState<ServerTurnStatus>("idle");
	const [stopRequested, setStopRequested] = useState(false);
	const watcherRef = useRef<ServerTurnWatcher | null>(null);
	const onFinishedRef = useRef(onFinished);
	onFinishedRef.current = onFinished;
	const ownsStreamRef = useRef(ownsStream);
	ownsStreamRef.current = ownsStream;

	useEffect(() => {
		setStatus("idle");
		setStopRequested(false);
		if (!threadId) {
			watcherRef.current = null;
			return;
		}
		const watcher = new ServerTurnWatcher(threadId, {
			fetchStatus: fetchServerTurnStatus,
			onStatus: setStatus,
			onFinished: () => onFinishedRef.current(),
			onStopRequested: setStopRequested,
			isHidden: () => typeof document !== "undefined" && document.visibilityState === "hidden",
		});
		watcherRef.current = watcher;
		if (ownsStreamRef.current) watcher.pause();
		else void watcher.check();

		const recheck = () => {
			if (document.visibilityState !== "hidden") void watcher.check();
		};
		const unsubscribeSignal = onTurnStatusCheckRequested((changedThreadId) => {
			if (changedThreadId === threadId) void watcher.check({ soon: true });
		});
		document.addEventListener("visibilitychange", recheck);
		window.addEventListener("focus", recheck);
		window.addEventListener("online", recheck);
		return () => {
			unsubscribeSignal();
			document.removeEventListener("visibilitychange", recheck);
			window.removeEventListener("focus", recheck);
			window.removeEventListener("online", recheck);
			watcher.dispose();
			if (watcherRef.current === watcher) watcherRef.current = null;
		};
	}, [threadId]);

	useEffect(() => {
		const watcher = watcherRef.current;
		if (!watcher) return;
		if (ownsStream) watcher.pause();
		else watcher.resume();
	}, [ownsStream]);

	// Only a change of the hint triggers a check; mounting already checked.
	const lastHintRef = useRef(respondingHint);
	useEffect(() => {
		if (lastHintRef.current === respondingHint) return;
		lastHintRef.current = respondingHint;
		if (!ownsStreamRef.current) void watcherRef.current?.check({ soon: true });
	}, [respondingHint]);

	const stop = useCallback(
		async (send?: () => Promise<void>) => {
			if (!threadId) return;
			const ask = send ?? (() => requestServerStop(threadId));
			const watcher = watcherRef.current;
			if (watcher) await watcher.stop(ask);
			else await ask();
		},
		[threadId]
	);

	return {
		status,
		running: status !== "idle",
		stopping: stopRequested || status === "cancelling",
		stop,
	};
}
