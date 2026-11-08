import { BRAND } from "./index";
import { currentSupportEmail } from "./support-email";

/**
 * A mailto: link to the operator with a problem report already written out.
 * Replaces the upstream "open a GitHub issue" links: problems here go to the
 * person who runs this server. Opening a mailto: link hands off to the mail
 * app and leaves the page where it is. The address is the admin-set support
 * email once loaded (see ./support-email), else the build's
 * NEXT_PUBLIC_SUPPORT_EMAIL. A server that publishes none gets `mailto:?...`:
 * the report is still written out, and the person fills in who it goes to.
 */
export function buildReportMailto(
	bodyLines: string[],
	brand = BRAND,
	to: string = currentSupportEmail(brand.contactEmail)
): string {
	const params = new URLSearchParams({
		subject: `${brand.name}: problem report`,
		body: bodyLines.join("\n"),
	});
	// URLSearchParams encodes spaces as "+", which mail clients show literally.
	return `mailto:${to}?${params.toString().replace(/\+/g, "%20")}`;
}
