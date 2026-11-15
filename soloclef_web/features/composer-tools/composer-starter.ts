import { atom } from "jotai";

/**
 * Words another part of the app asks the chat box to start with (the
 * Automations page's "Create via chat"). The composer applies them once,
 * through its editor as a picked example prompt is, and clears this; only
 * to an empty box, so nothing the person typed is overwritten. Never sent.
 */
export const composerStarterAtom = atom<string | null>(null);
