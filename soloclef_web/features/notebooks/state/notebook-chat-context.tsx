"use client";

import { createContext, type ReactNode, useContext } from "react";

/**
 * What the shared chat components need to know when a thread is rendered
 * inside a notebook: the notebook itself, how many sources the conversation
 * is grounded in, the conversation on screen, and ways to go add sources.
 * Absent outside notebooks, so the regular chat renders exactly as before.
 */
export interface NotebookChatContextValue {
	notebookId: number;
	title: string;
	icon: string | null;
	createdAt: string | null;
	sourceCount: number;
	enabledSourceCount: number;
	/** The conversation on screen: what Save to note links a kept answer back to. */
	threadId: number | null;
	openSources?: () => void;
	/** Opens the Sources panel with its "Add sources" dialog. */
	addSources?: () => void;
	/**
	 * Opens "Add sources" on its Upload tab. A notebook chat reads only its
	 * sources, so the composer's "Upload files" adds sources here instead of
	 * uploading to the account's files where this chat could not see them.
	 */
	uploadSources?: () => void;
	/** Opens one of the notebook's sources, by its document, in the Sources panel. */
	openSource?: (documentId: number) => void;
	/** Opens the Sources panel on its "Search the web" box. */
	openResearch?: () => void;
	/** Brings the chat into view (the phone's Chat tab). */
	showChat?: () => void;
	/**
	 * Opens something the chat made in Studio's output view, as Studio opens
	 * it. False when the artifact is not one of the notebook's Studio outputs.
	 */
	openArtifactOutput?: (artifactId: number) => boolean;
}

const NotebookChatContext = createContext<NotebookChatContextValue | null>(null);

export function NotebookChatProvider({
	value,
	children,
}: {
	value: NotebookChatContextValue;
	children: ReactNode;
}) {
	return <NotebookChatContext.Provider value={value}>{children}</NotebookChatContext.Provider>;
}

export function useNotebookChat(): NotebookChatContextValue | null {
	return useContext(NotebookChatContext);
}
