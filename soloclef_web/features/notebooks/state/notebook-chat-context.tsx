"use client";

import { createContext, type ReactNode, useContext } from "react";

/**
 * What the shared chat components need to know when a thread is rendered
 * inside a notebook: the notebook itself, how many sources the conversation
 * is grounded in, and ways to keep an answer as a note or go add sources.
 * Absent outside notebooks, so the regular chat renders exactly as before.
 */
export interface NotebookChatContextValue {
	workspaceId: number;
	notebookId: number;
	title: string;
	icon: string | null;
	createdAt: string | null;
	sourceCount: number;
	enabledSourceCount: number;
	saveToNote: (markdown: string) => Promise<void>;
	openSources?: () => void;
	/** Opens the Sources panel with its "Add sources" dialog. */
	addSources?: () => void;
	/**
	 * Opens "Add sources" on its Upload tab. A notebook chat reads only its
	 * sources, so the composer's "Upload files" adds sources here instead of
	 * uploading to the workspace where this chat could not see them.
	 */
	uploadSources?: () => void;
	/** Opens the Sources panel on its "Search the web" box. */
	openResearch?: () => void;
	/** Brings the chat into view (the phone's Chat tab). */
	showChat?: () => void;
	/** A turn ended: an unnamed notebook may just have named itself from the question. */
	watchTitle?: () => void;
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
