import type { Metadata } from "next";
import Link from "next/link";
import { ContactEmail } from "@/components/site/contact-email";
import { PageHero } from "@/components/site/site-sections";
import { BRAND, brandTitle, OG_IMAGES, siteUrl } from "@/lib/brand";

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

export default function ContactPage() {
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
					<ContactEmail />
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
