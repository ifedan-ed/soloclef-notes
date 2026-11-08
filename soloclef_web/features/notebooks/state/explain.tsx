"use client";

import { useAtomValue, useSetAtom } from "jotai";
import { createContext, type ReactNode, useCallback, useContext } from "react";
import { closeArtifactPanelAtom } from "@/features/artifacts/state/artifact-panel.atom";
import { useMediaQuery } from "@/hooks/use-media-query";
import { notebookAskAtom } from "./notebook-ask.atom";

/**
 * Where a Studio viewer is shown decides what "Explain" does to it. The
 * report view steps aside (it covers the chat) and can be reopened where it
 * was; the Studio column keeps the output open beside the chat; a phone
 * switches to the Chat tab (the notebook chat's ``showChat``), and the
 * artifact sheet a chat card opens closes, since it covers the chat.
 */
const ExplainHostContext = createContext<(() => void) | null>(null);

export function ExplainHostProvider({
	onExplain,
	children,
}: {
	/** Called just before the message is sent. */
	onExplain: () => void;
	children: ReactNode;
}) {
	return <ExplainHostContext.Provider value={onExplain}>{children}</ExplainHostContext.Provider>;
}

/**
 * Sends a message to the open notebook's chat, which answers from the
 * notebook's sources with citations (if an answer is still streaming, the
 * message waits in the composer instead). Null outside a notebook, where
 * viewers offer no Explain.
 */
export function useExplain(): ((message: string) => void) | null {
	const ask = useAtomValue(notebookAskAtom);
	const host = useContext(ExplainHostContext);
	const closePanel = useSetAtom(closeArtifactPanelAtom);
	const isDesktop = useMediaQuery("(min-width: 1024px)");
	const explain = useCallback(
		(message: string) => {
			if (!ask) return;
			host?.();
			ask.send(message);
			if (!isDesktop && !host) closePanel();
		},
		[ask, host, closePanel, isDesktop]
	);
	return ask ? explain : null;
}
