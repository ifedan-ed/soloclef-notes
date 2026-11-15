"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { type MailDraftView, mailDraftApi } from "../api/mail-draft-api";
import { type SendRefusal, sendRefusal } from "../lib/send-refusal";

export type DraftNotice = { messageKey: string; detail?: string; tone: "info" | "error" };

/** How a draft left the waiting list: sent, or discarded. */
export type SettledOutcome = "sent" | "discarded";

type State =
	| { phase: "loading" }
	| { phase: "unavailable" }
	| {
			phase: "ready";
			view: MailDraftView;
			busy: "send" | "discard" | null;
			notice: DraftNotice | null;
	  };

/**
 * One draft card's state: the stored draft and its confirmation, fetched
 * from the server, and the two things the person can do with it.
 *
 * Nothing here sends on its own. The draft is fetched on mount (which asks
 * for a confirmation, never a send); send() runs only from a click, refuses
 * while a request is in flight, and every refusal ends with the draft shown
 * again for the person to check, with no retry made for them.
 */
export function useMailDraft(
	draftId: string,
	/**
	 * Told once a press of Send or Discard has ended the draft: the Drafts
	 * waiting page takes it off its list. Not told when the draft was already
	 * settled on load, which the page learns when it reads its list again.
	 */
	onSettled?: (outcome: SettledOutcome) => void
) {
	const [state, setState] = useState<State>({ phase: "loading" });
	// The in-flight guard is a ref as well as state: two clicks inside one
	// render would both see the old state, and only one may reach the server.
	const inFlight = useRef(false);

	const load = useCallback(
		async (notice: DraftNotice | null) => {
			try {
				const view = await mailDraftApi.confirm(draftId);
				setState({ phase: "ready", view, busy: null, notice });
			} catch {
				setState({ phase: "unavailable" });
			}
		},
		[draftId]
	);

	useEffect(() => {
		void load(null);
	}, [load]);

	// acknowledged is the unusual points the person ticked; the server checks
	// them against the confirmation, so the card cannot send past one.
	const send = useCallback(
		async (acknowledged: string[]) => {
			if (inFlight.current || state.phase !== "ready") return;
			const { view } = state;
			if (view.confirmation === null) return;
			inFlight.current = true;
			setState({ ...state, busy: "send", notice: null });
			try {
				const sent = await mailDraftApi.send(view.draft.id, view.confirmation, acknowledged);
				setState({ phase: "ready", view: sent, busy: null, notice: null });
				if (sent.draft.status === "sent") onSettled?.("sent");
			} catch (error) {
				const refusal: SendRefusal = sendRefusal(error);
				const notice: DraftNotice = {
					messageKey: refusal.messageKey,
					detail: refusal.detail,
					tone: refusal.next === "reconfirm" ? "info" : "error",
				};
				if (refusal.next === "stay") {
					setState({ phase: "ready", view, busy: null, notice });
				} else {
					// Show what the server holds now, with a new confirmation when it
					// can still be sent: the person checks it and presses Send again.
					await load(notice);
				}
			} finally {
				inFlight.current = false;
			}
		},
		[state, load, onSettled]
	);

	const discard = useCallback(async () => {
		if (inFlight.current || state.phase !== "ready") return;
		inFlight.current = true;
		setState({ ...state, busy: "discard", notice: null });
		try {
			const discarded = await mailDraftApi.discard(state.view.draft.id);
			setState({ phase: "ready", view: discarded, busy: null, notice: null });
			if (discarded.draft.status === "discarded") onSettled?.("discarded");
		} catch {
			setState({
				...state,
				busy: null,
				notice: { messageKey: "discard_failed", tone: "error" },
			});
		} finally {
			inFlight.current = false;
		}
	}, [state, onSettled]);

	return { state, send, discard };
}
