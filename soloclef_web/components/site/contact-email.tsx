"use client";

import { useEffect, useState } from "react";
import { CopyTextButton } from "@/components/site/copy-text-button";
import { currentSupportEmail, loadSupportEmail } from "@/lib/brand/support-email";

/**
 * The contact page's address: the one the API publishes
 * (SOLOCLEF_API_SUPPORT_EMAIL), read in the browser because the page is a
 * static file written at build time. An address set after the build still
 * shows, as it did when a server drew this page.
 *
 * Until the answer is in, the build's address is shown if it has one. If it
 * has none, nothing is shown yet, rather than "no contact address" for a
 * moment on a server that does have one.
 */
export function ContactEmail() {
	const built = currentSupportEmail();
	const [email, setEmail] = useState<string | null>(built || null);
	const [settled, setSettled] = useState(Boolean(built));

	useEffect(() => {
		let live = true;
		void loadSupportEmail().then((value) => {
			if (!live) return;
			setEmail(value || null);
			setSettled(true);
		});
		return () => {
			live = false;
		};
	}, []);

	if (!settled) return <div className="mt-3 h-9" aria-busy="true" />;
	if (!email) {
		return (
			<p className="ss-home-body mt-3 text-sm" data-testid="contact-email-none">
				This server has no contact address yet. Ask the person who runs it.
			</p>
		);
	}
	return (
		<div className="mt-3 flex flex-wrap items-center gap-3">
			<span
				className="select-all break-all font-mono text-lg text-[color:var(--foreground)]"
				data-testid="contact-email"
			>
				{email}
			</span>
			<CopyTextButton text={email} label="Copy address" />
		</div>
	);
}
