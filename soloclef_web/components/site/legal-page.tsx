import { PageHero } from "@/components/site/site-sections";

/** The frame for the privacy and terms pages: a heading, a date, then prose. */
export function LegalPage({
	title,
	updated,
	lede,
	children,
}: {
	title: string;
	updated: string;
	lede: string;
	children: React.ReactNode;
}) {
	return (
		<>
			<PageHero eyebrow={`Last updated ${updated}`} title={title} lede={lede} />
			<section className="ss-home-rule">
				<div className="ss-home-pad ss-home-prose max-w-3xl pt-4 pb-16">{children}</div>
			</section>
		</>
	);
}
