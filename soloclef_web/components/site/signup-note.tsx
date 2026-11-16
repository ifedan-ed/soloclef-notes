"use client";

import Link from "next/link";
import { REQUEST_ACCESS_HREF, SIGNUPS_OPEN } from "@/components/site/site-content";
import { useSupportEmail } from "@/hooks/use-support-email";

/**
 * The home and features pages' note under the hero while sign-ups are
 * closed. "Request access" is offered only where there is someone to ask:
 * a server that publishes no contact address (useSupportEmail) gets the
 * note alone, not a link to a contact page with nobody on it.
 */
export function SignupNote() {
	const { email } = useSupportEmail();
	if (SIGNUPS_OPEN) return null;
	return (
		<p className="mt-4 text-sm text-[color:var(--muted-foreground)]" data-testid="signup-note">
			New accounts are not open yet.
			{email ? (
				<>
					{" "}
					<Link className="ss-home-link" href={REQUEST_ACCESS_HREF}>
						Request access
					</Link>
				</>
			) : null}
		</p>
	);
}
