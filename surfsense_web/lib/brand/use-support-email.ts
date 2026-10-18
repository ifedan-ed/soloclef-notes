"use client";

import { useEffect, useState } from "react";
import { currentSupportEmail, loadSupportEmail } from "./support-email";

/** The Report Issue address: the default at first, the admin's once loaded. */
export function useSupportEmail(): string {
	const [email, setEmail] = useState(currentSupportEmail);
	useEffect(() => {
		let live = true;
		loadSupportEmail().then((value) => {
			if (live) setEmail(value);
		});
		return () => {
			live = false;
		};
	}, []);
	return email;
}
