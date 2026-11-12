import { atom } from "jotai";
import { rightPanelCollapsedAtom, rightPanelTabAtom } from "@/atoms/layout/right-panel.atom";

interface CitationPanelState {
	isOpen: boolean;
	/** The knowledge-base chunk the citation panel is showing. */
	chunkId: number | null;
}

const initialState: CitationPanelState = {
	isOpen: false,
	chunkId: null,
};

export const citationPanelAtom = atom<CitationPanelState>(initialState);

export const citationPanelOpenAtom = atom((get) => get(citationPanelAtom).isOpen);

const preCitationCollapsedAtom = atom<boolean | null>(null);

export const openCitationPanelAtom = atom(null, (get, set, payload: { chunkId: number }) => {
	// Remember whether the panel was collapsed before the first citation opened
	// it, so closing the citation puts the layout back the way it was.
	if (!get(citationPanelAtom).isOpen) {
		set(preCitationCollapsedAtom, get(rightPanelCollapsedAtom));
	}
	set(citationPanelAtom, { isOpen: true, chunkId: payload.chunkId });
	set(rightPanelTabAtom, "citation");
	set(rightPanelCollapsedAtom, false);
});

export const closeCitationPanelAtom = atom(null, (get, set) => {
	set(citationPanelAtom, initialState);
	set(rightPanelTabAtom, "none");
	const prev = get(preCitationCollapsedAtom);
	if (prev !== null) {
		set(rightPanelCollapsedAtom, prev);
		set(preCitationCollapsedAtom, null);
	}
});
