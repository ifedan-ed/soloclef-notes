import { atom } from "jotai";

/**
 * Sends a question to the open notebook's chat. Studio viewers (the mind
 * map's "click a topic to ask about it") open in the artifact panel, outside
 * the chat's runtime, so the notebook chat registers its sender here while
 * it is mounted. Null outside a notebook: viewers then offer no asking.
 */
export interface NotebookAsk {
	send: (question: string) => void;
}

export const notebookAskAtom = atom<NotebookAsk | null>(null);
