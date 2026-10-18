"use client";

import { useCallback, useEffect, useReducer, useRef } from "react";
import { ResearchRequestError, streamResearchSearch } from "../api/research-api";
import { idleResearch, researchReducer } from "../lib/research-state";
import type { ResearchRequest } from "../model/research";

/**
 * One web search for new sources at a time: starting another, clearing, or
 * leaving the page aborts the one in flight so its events never land late.
 */
export function useResearchSearch(workspaceId: number, notebookId: number) {
	const [state, dispatch] = useReducer(researchReducer, idleResearch);
	const controllerRef = useRef<AbortController | null>(null);

	const abortInFlight = useCallback(() => {
		controllerRef.current?.abort();
		controllerRef.current = null;
	}, []);

	useEffect(() => abortInFlight, [abortInFlight]);

	const search = useCallback(
		async (request: ResearchRequest) => {
			abortInFlight();
			const controller = new AbortController();
			controllerRef.current = controller;
			dispatch({ type: "start", request });
			try {
				await streamResearchSearch({
					workspaceId,
					notebookId,
					request,
					signal: controller.signal,
					onEvent: (event) => {
						if (!controller.signal.aborted) dispatch({ type: "event", event });
					},
				});
				if (!controller.signal.aborted) dispatch({ type: "closed" });
			} catch (error) {
				if (controller.signal.aborted) return;
				dispatch({
					type: "failed",
					message: error instanceof ResearchRequestError ? error.detail : null,
				});
			} finally {
				if (controllerRef.current === controller) controllerRef.current = null;
			}
		},
		[workspaceId, notebookId, abortInFlight]
	);

	const clear = useCallback(() => {
		abortInFlight();
		dispatch({ type: "clear" });
	}, [abortInFlight]);

	return { state, search, clear };
}
