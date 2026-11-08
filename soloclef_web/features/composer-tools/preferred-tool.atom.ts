import { atom } from "jotai";
import type { ComposerToolId } from "./composer-tools";

/** The tool the composer forces for the next message only. */
export const preferredToolAtom = atom<ComposerToolId | null>(null);

/** Immutable submit-time snapshot consumed once by startNewChat. */
export const submittedPreferredToolAtom = atom<ComposerToolId | null>(null);
