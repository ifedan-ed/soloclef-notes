// The Plate editor's code is large and loads on its own, after the page. A
// page that opens notes asks for it as it mounts (preloadPlateEditor), so a
// note draws whole the moment its text is there. next/dynamic could not do
// that: it shows its placeholder for a moment even when the code is already
// loaded, and that second placeholder, after the note's own, was part of
// the flicker of opening a note.
import { useEffect, useState } from "react";

type PlateEditorModule = typeof import("./plate-editor");

let loaded: PlateEditorModule | null = null;
let loading: Promise<PlateEditorModule> | null = null;

/** Starts loading the editor's code (once); resolves when it is there. */
export function preloadPlateEditor(): Promise<PlateEditorModule> {
	loading ??= import("./plate-editor").then(
		(module) => {
			loaded = module;
			return module;
		},
		(error: unknown) => {
			// A failed load (a deploy replaced the chunk, the network dropped) is tried again next time.
			loading = null;
			throw error;
		}
	);
	return loading;
}

/** The PlateEditor component, or null while its code loads; a failed load goes to the error boundary. */
export function usePlateEditorComponent(): PlateEditorModule["PlateEditor"] | null {
	const [module, setModule] = useState<PlateEditorModule | null>(loaded);
	const [failure, setFailure] = useState<unknown>(null);
	useEffect(() => {
		if (module) return;
		let live = true;
		preloadPlateEditor().then(
			(next) => live && setModule(next),
			(error: unknown) => live && setFailure(error)
		);
		return () => {
			live = false;
		};
	}, [module]);
	if (failure) throw failure;
	return module?.PlateEditor ?? null;
}
