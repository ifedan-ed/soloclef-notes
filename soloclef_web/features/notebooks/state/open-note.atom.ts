import { atom } from "jotai";

/**
 * A note to open in a notebook's Notes tab, asked for from outside the
 * panel (the chat's Save to note). The panel opens it and clears this.
 */
export interface NoteToOpen {
	notebookId: number;
	noteId: number;
}

export const noteToOpenAtom = atom<NoteToOpen | null>(null);
