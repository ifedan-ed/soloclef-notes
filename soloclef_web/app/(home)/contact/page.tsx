import type { Metadata } from "next";
import Link from "next/link";
import { CopyTextButton } from "@/components/site/copy-text-button";
import { PageHero } from "@/components/site/site-sections";
import { BRAND, brandTitle, OG_IMAGES, siteUrl } from "@/lib/brand";
import { loadSupportEmailOnServer } from "@/lib/brand/load-support-email";

const TITLE = brandTitle("Contact");
const DESCRIPTION = `How to reach the person who runs ${BRAND.name}.`;

export const metadata: Metadata = {
	title: TITLE,
	description: DESCRIPTION,
	alternates: { canonical: siteUrl("/contact") },
	openGraph: {
		title: TITLE,
		description: DESCRIPTION,
		url: siteUrl("/contact"),
		siteName: BRAND.name,
		images: OG_IMAGES,
	},
};

export default async function ContactPage() {
	// The admin-set support address (Billing -> Admin -> Settings), else
	// SUPPORT_EMAIL; empty on a server that has published none.
	const email = await loadSupportEmailOnServer();
	return (
		<>
			<PageHero
				eyebrow="Contact"
				title="Write to a person"
				lede={`${BRAND.name} is run by one person. Questions, problems, account requests and feedback all go to the same address.`}
			/>
			<section className="ss-home-rule">
				<div className="ss-home-pad py-10">
					<p className="ss-home-eyebrow">Email</p>
					{email ? (
						<div className="mt-3 flex flex-wrap items-center gap-3">
							<span
								className="select-all break-all font-mono text-lg text-[color:var(--foreground)]"
								data-testid="contact-email"
							>
								{email}
							</span>
							<CopyTextButton text={email} label="Copy address" />
						</div>
					) : (
						<p className="ss-home-body mt-3 text-sm" data-testid="contact-email-none">
							This server has no contact address yet. Ask the person who runs it.
						</p>
					)}
					<p className="ss-home-body mt-6 max-w-2xl text-sm">
						If something went wrong in the app, say which notebook and roughly when, and what you
						expected to happen. Many answers are already in the{" "}
						<Link className="ss-home-link" href="/docs">
							docs
						</Link>
						.
					</p>
				</div>
			</section>
		</>
	);
}
