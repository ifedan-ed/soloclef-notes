"use client";

import { useEffect, useState } from "react";
import { currentSupportEmail, loadSupportEmail } from "@/lib/brand/support-email";

/**
 * The server's contact address (SOLOCLEF_API_SUPPORT_EMAIL) once it is known,
 * or "" when the server publishes none. A server may run without one on
 * purpose (an address on a public page draws bots), so every "Request
 * access" link waits for this and is left out when it is empty: a link to a
 * contact page with nobody to contact is a dead end.
 */
export function useSupportEmail(): { email: string; settled: boolean } {
	const built = currentSupportEmail();
	const [email, setEmail] = useState(built);
	const [settled, setSettled] = useState(Boolean(built));
	useEffect(() => {
		let live = true;
		void loadSupportEmail().then((value) => {
			if (!live) return;
			setEmail(value);
			setSettled(true);
		});
		return () => {
			live = false;
		};
	}, []);
	return { email, settled };
}
