"use client";

import { useSetAtom } from "jotai";
import { useEffect } from "react";
import { composerStarterAtom } from "@/features/composer-tools/composer-starter";
import {
	AUTOMATION_STARTER,
	COMPOSE_AUTOMATION,
	COMPOSE_PARAM,
} from "@/lib/automations/create-via-chat";
import { useNotebookChat } from "../../state/notebook-chat-context";

/**
 * Starts the chat box with the words another page asked for
 * (``?compose=automation``, from the Automations page's "Create via chat"),
 * then takes the parameter out of the address so a reload or a shared link
 * does not type it again. It types and never sends: the person finishes the
 * request. An unknown value is dropped and types nothing.
 */
export function NotebookComposeBridge() {
	const setStarter = useSetAtom(composerStarterAtom);
	const showChat = useNotebookChat()?.showChat;

	useEffect(() => {
		const asked = new URL(window.location.href).searchParams.get(COMPOSE_PARAM);
		if (asked === null) return;
		if (asked === COMPOSE_AUTOMATION) {
			// On a phone the notebook opens on its sources; the chat is a tab.
			showChat?.();
			setStarter(AUTOMATION_STARTER);
		}
		// Deferred, as NotebookChat's own clean-up is: the router writes the
		// address after effects run, and would put the parameter back.
		const timer = window.setTimeout(() => {
			const url = new URL(window.location.href);
			url.searchParams.delete(COMPOSE_PARAM);
			window.history.replaceState(window.history.state, "", url.toString());
		}, 0);
		return () => window.clearTimeout(timer);
	}, [setStarter, showChat]);

	return null;
}
