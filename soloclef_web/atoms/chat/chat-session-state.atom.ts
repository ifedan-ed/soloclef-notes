"use client";

import { atom } from "jotai";

export interface ChatSessionStateData {
	threadId: number;
	/** A turn runs on the thread, on this page or any other (turn-status). */
	isAiResponding: boolean;
}

/**
 * Global chat session state atom.
 * Updated by useChatSessionStateSync hook, read anywhere.
 */
export const chatSessionStateAtom = atom<ChatSessionStateData | null>(null);
