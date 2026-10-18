import { atom } from "jotai";
import { rightPanelCollapsedAtom, rightPanelTabAtom } from "@/atoms/layout/right-panel.atom";

interface DocumentViewerState {
	isOpen: boolean;
	documentId: number | null;
	workspaceId: number | null;
	title: string;
	/** 1-based page of the original PDF to open at (a citation's page), or null for the first. */
	initialPage: number | null;
	/** Text to find and mark on that page (the cited chunk), or null. */
	highlightText: string | null;
}

export interface OpenDocumentViewerPayload {
	documentId: number;
	workspaceId: number;
	title: string;
	initialPage?: number | null;
	highlightText?: string | null;
}

const initialState: DocumentViewerState = {
	isOpen: false,
	documentId: null,
	workspaceId: null,
	title: "",
	initialPage: null,
	highlightText: null,
};

export const documentViewerAtom = atom<DocumentViewerState>(initialState);
const previousCollapsedAtom = atom<boolean | null>(null);

export const openDocumentViewerAtom = atom(
	null,
	(
		get,
		set,
		{ documentId, workspaceId, title, initialPage, highlightText }: OpenDocumentViewerPayload
	) => {
		if (!get(documentViewerAtom).isOpen) {
			set(previousCollapsedAtom, get(rightPanelCollapsedAtom));
		}
		set(documentViewerAtom, {
			isOpen: true,
			documentId,
			workspaceId,
			title,
			initialPage: initialPage && initialPage > 0 ? initialPage : null,
			highlightText: highlightText || null,
		});
		set(rightPanelTabAtom, "document");
		set(rightPanelCollapsedAtom, false);
	}
);

export const closeDocumentViewerAtom = atom(null, (get, set) => {
	set(documentViewerAtom, initialState);
	set(rightPanelTabAtom, "none");
	const previous = get(previousCollapsedAtom);
	if (previous !== null) {
		set(rightPanelCollapsedAtom, previous);
		set(previousCollapsedAtom, null);
	}
});
