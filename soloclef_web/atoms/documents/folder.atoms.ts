"use client";

import { atom } from "jotai";
import { atomWithStorage } from "jotai/utils";

/**
 * The folder IDs currently expanded in the tree. Persisted to localStorage so
 * expand/collapse state survives page refreshes. One list: an account has one
 * folder tree. The storage key is new because the old value was a map keyed
 * by workspace, which would not read as a list.
 */
export const expandedFolderIdsAtom = atomWithStorage<number[]>("soloclef:expandedFolderIds", []);

/**
 * Expanded folder keys for the Local filesystem tree. Persisted so local tree
 * expansion survives remounts/reloads.
 */
export const localExpandedFolderKeysAtom = atomWithStorage<string[]>(
	"soloclef:localExpandedFolderKeys",
	[]
);

/**
 * Folder currently being renamed (inline edit mode).
 * null means no folder is being renamed.
 */
export const renamingFolderIdAtom = atom<number | null>(null);

/**
 * Bumped whenever a folder is (un)watched outside DocumentRightPanel (e.g. the
 * sidebar-footer "Watch Local Folder" button) so the tree re-reads Electron's
 * watched-folder list and shows the sync badge without a reload.
 */
export const watchedFoldersRefreshAtom = atom(0);
