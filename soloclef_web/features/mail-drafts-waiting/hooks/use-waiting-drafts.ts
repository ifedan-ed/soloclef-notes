"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { type WaitingDrafts, waitingDraftsApi, waitingDraftsKey } from "../api/waiting-drafts-api";

/**
 * The drafts still waiting, and a way to take one off the list the moment
 * it is sent or discarded.
 *
 * The list is read again whenever the page regains focus (the query's
 * default), which is when a draft sent from a chat in another tab, or one
 * an automation just made, would show up; there is no polling, because a
 * draft only changes when its owner acts on it.
 */
export function useWaitingDrafts() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: waitingDraftsKey,
		queryFn: waitingDraftsApi.list,
		// A draft can be sent from the chat as well; a list a minute old
		// could offer one already gone, which its card would then say.
		staleTime: 0,
	});

	// Taken off at once rather than refetched: the person just watched it
	// go, and a refetch would redraw every other card (each asking the
	// server for a new go-ahead) for nothing.
	const settle = useCallback(
		(draftId: string) => {
			const list = queryClient.setQueryData<WaitingDrafts>(waitingDraftsKey, (current) =>
				current
					? { ...current, drafts: current.drafts.filter((draft) => draft.id !== draftId) }
					: current
			);
			// The last shown one is gone and the server held more back: now
			// is when the rest are worth fetching.
			if (list?.more && list.drafts.length === 0) {
				void queryClient.invalidateQueries({ queryKey: waitingDraftsKey });
			}
		},
		[queryClient]
	);

	return { ...query, settle };
}
