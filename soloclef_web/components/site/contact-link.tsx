"use client";

import Link from "next/link";
import { CONTACT_HREF } from "@/components/site/site-content";
import { useSupportEmail } from "@/hooks/use-support-email";

/**
 * A "Contact" link for pages drawn at build time (the 404 page), shown only
 * once the server is known to publish a contact address: the address is read
 * in the browser, and without one the contact page is a dead end.
 */
export function ContactLink({ className }: { className?: string }) {
	const { email } = useSupportEmail();
	if (!email) return null;
	return (
		<Link href={CONTACT_HREF} className={className}>
			Contact
		</Link>
	);
}
