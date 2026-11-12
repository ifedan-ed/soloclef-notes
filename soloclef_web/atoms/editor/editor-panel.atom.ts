import { atom } from "jotai";
import { rightPanelCollapsedAtom, rightPanelTabAtom } from "@/atoms/layout/right-panel.atom";

interface EditorPanelState {
	isOpen: boolean;
	kind: "local_file" | "memory";
	localFilePath: string | null;
	memoryScope: "user" | null;
	title: string | null;
}

const initialState: EditorPanelState = {
	isOpen: false,
	kind: "memory",
	localFilePath: null,
	memoryScope: null,
	title: null,
};

export const editorPanelAtom = atom<EditorPanelState>(initialState);

export const editorPanelOpenAtom = atom((get) => get(editorPanelAtom).isOpen);

const preEditorCollapsedAtom = atom<boolean | null>(null);

export const openEditorPanelAtom = atom(
	null,
	(
		get,
		set,
		payload:
			| {
					kind: "local_file";
					localFilePath: string;
					title?: string;
			  }
			| {
					kind: "memory";
					memoryScope: "user";
					title?: string;
			  }
	) => {
		if (!get(editorPanelAtom).isOpen) {
			set(preEditorCollapsedAtom, get(rightPanelCollapsedAtom));
		}
		if (payload.kind === "local_file") {
			set(editorPanelAtom, {
				isOpen: true,
				kind: "local_file",
				localFilePath: payload.localFilePath,
				memoryScope: null,
				title: payload.title ?? null,
			});
			set(rightPanelTabAtom, "editor");
			set(rightPanelCollapsedAtom, false);
			return;
		}
		set(editorPanelAtom, {
			isOpen: true,
			kind: "memory",
			localFilePath: null,
			memoryScope: payload.memoryScope,
			title: payload.title ?? null,
		});
		set(rightPanelTabAtom, "editor");
		set(rightPanelCollapsedAtom, false);
	}
);

export const closeEditorPanelAtom = atom(null, (get, set) => {
	set(editorPanelAtom, initialState);
	set(rightPanelTabAtom, "none");
	const prev = get(preEditorCollapsedAtom);
	if (prev !== null) {
		set(rightPanelCollapsedAtom, prev);
		set(preEditorCollapsedAtom, null);
	}
});
