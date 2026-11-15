"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { AutomationCreateRequest } from "@/contracts/types/automation.types";
import type { AutomationDraft } from "@/contracts/types/automation-draft.types";
import { automationDraftsApiService } from "@/lib/apis/automation-drafts-api.service";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import { AppError } from "@/lib/error";

type State =
	| { phase: "loading" }
	| { phase: "unavailable" }
	| {
			phase: "ready";
			draft: AutomationDraft;
			busy: "create" | "discard" | null;
			/** The server's sentence for the last Create or Discard it refused. */
			refusal: string | null;
	  };

/** The server's own sentence for a refusal, or a plain one when it gave none. */
function refusalOf(error: unknown): string {
	if (error instanceof AppError && error.message) return error.message;
	return "That did not go through. Check your connection and try again.";
}

/**
 * One draft card's state: the draft as the server holds it, and the two
 * things the person can do with it.
 *
 * Nothing is saved on its own. The draft is read on mount; create() runs
 * only from a click, refuses while a request is in flight, and sends the
 * person's edited request with the draft's id, so the server creates it at
 * most once (it marks the draft created in the same transaction). A refusal
 * leaves the person's edits on the card with the server's sentence.
 */
export function useAutomationDraft(draftId: string) {
	const queryClient = useQueryClient();
	const [state, setState] = useState<State>({ phase: "loading" });
	// A ref as well as state: two clicks inside one render would both see the
	// old state, and only one may reach the server.
	const inFlight = useRef(false);

	const load = useCallback(
		async (refusal: string | null) => {
			try {
				const draft = await automationDraftsApiService.getDraft(draftId);
				setState({ phase: "ready", draft, busy: null, refusal });
			} catch {
				setState({ phase: "unavailable" });
			}
		},
		[draftId]
	);

	useEffect(() => {
		void load(null);
	}, [load]);

	const act = useCallback(
		async (kind: "create" | "discard", run: () => Promise<unknown>) => {
			if (inFlight.current || state.phase !== "ready") return;
			inFlight.current = true;
			setState({ ...state, busy: kind, refusal: null });
			try {
				await run();
				if (kind === "create") {
					// The Automations list shows it at once, wherever it is open.
					await queryClient.invalidateQueries({ queryKey: ["automations", "list"] });
				}
				await load(null);
			} catch (error) {
				// The draft is read again (another tab may have created it) with
				// the refusal beside it; the card keeps the person's edits.
				await load(refusalOf(error));
			} finally {
				inFlight.current = false;
			}
		},
		[state, load, queryClient]
	);

	const create = useCallback(
		(request: AutomationCreateRequest) =>
			act("create", () =>
				automationsApiService.createAutomation({ ...request, draft_id: draftId })
			),
		[act, draftId]
	);
	const discard = useCallback(
		() => act("discard", () => automationDraftsApiService.discardDraft(draftId)),
		[act, draftId]
	);

	return { state, create, discard };
}
