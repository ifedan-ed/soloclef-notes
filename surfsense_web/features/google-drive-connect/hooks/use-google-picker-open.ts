"use client";

import { useEffect, useState } from "react";
import { PICKER_VISIBILITY_EVENT } from "../lib/google-picker";

/**
 * Whether the Google Picker is on screen. A modal dialog under it must stop
 * being modal meanwhile: the Picker lives outside the dialog, and a modal
 * dialog blocks pointer events and focus everywhere else.
 */
export function useGooglePickerOpen(): boolean {
	const [open, setOpen] = useState(false);
	useEffect(() => {
		const onChange = (event: Event) => setOpen(Boolean((event as CustomEvent<boolean>).detail));
		window.addEventListener(PICKER_VISIBILITY_EVENT, onChange);
		return () => window.removeEventListener(PICKER_VISIBILITY_EVENT, onChange);
	}, []);
	return open;
}
