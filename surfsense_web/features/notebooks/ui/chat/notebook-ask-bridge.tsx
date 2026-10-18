"use client";

import { useThreadRuntime } from "@assistant-ui/react";
import { useSetAtom } from "jotai";
import { useEffect } from "react";
import { notebookAskAtom } from "../../state/notebook-ask.atom";
import { useNotebookChat } from "../../state/notebook-chat-context";

/**
 * Lets a Studio viewer ask this notebook's chat a question, as a follow-up
 * chip does: the question is typed into the composer and sent. While an
 * answer is still streaming it is only put in the composer, to send next.
 */
export function NotebookAskBridge() {
	const thread = useThreadRuntime();
	const notebook = useNotebookChat();
	const setAsk = useSetAtom(notebookAskAtom);
	const showChat = notebook?.showChat;

	useEffect(() => {
		const ask = {
			send: (question: string) => {
				showChat?.();
				thread.composer.setText(question);
				if (!thread.getState().isRunning) thread.composer.send();
			},
		};
		setAsk(ask);
		return () => setAsk((current) => (current === ask ? null : current));
	}, [thread, setAsk, showChat]);

	return null;
}
