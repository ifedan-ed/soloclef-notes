import Link from "next/link";
import { BRAND } from "@/lib/brand";
import MARK from "@/lib/brand/mark.json";
import { cn } from "@/lib/utils";

/**
 * The product mark: an ink note tile with a folded accent corner, three lines
 * of text and a citation dot. Drawn from `lib/brand/mark.json`, the same
 * geometry `scripts/brand-assets.mjs` uses for the favicon, app icons and OG
 * image, so the two never drift.
 *
 * Inline rather than an <img>, so it is painted with the first HTML and never
 * pops in after an image request. It keeps its own colours in both themes.
 */
export function BrandMark({ className, title }: { className?: string; title?: string }) {
	const { ink, accent, accentOnDark } = BRAND.colors;
	const [dx, dy, dr] = MARK.dot;
	return (
		<svg
			viewBox={`0 0 ${MARK.viewBox} ${MARK.viewBox}`}
			className={cn("shrink-0 select-none", className)}
			role={title ? "img" : undefined}
			aria-hidden={title ? undefined : true}
			aria-label={title}
		>
			<rect width={MARK.viewBox} height={MARK.viewBox} rx={MARK.radius} fill={ink} />
			<path d={MARK.fold} fill={accent} />
			<g fill="none" stroke="#ffffff" strokeWidth={MARK.lineWidth} strokeLinecap="round">
				{MARK.lines.map(([x1, y, x2]) => (
					<path key={y} d={`M${x1} ${y}H${x2}`} />
				))}
			</g>
			<circle cx={dx} cy={dy} r={dr} fill={accentOnDark} />
		</svg>
	);
}

export const Logo = ({
	className,
	disableLink = false,
}: {
	className?: string;
	disableLink?: boolean;
	/** Kept for call sites written against the old <Image> logo; the mark is inline now. */
	priority?: boolean;
}) => {
	const mark = <BrandMark className={className} title={BRAND.name} />;

	if (disableLink) {
		return mark;
	}

	return (
		<Link href="/" className="select-none" aria-label={BRAND.name}>
			<BrandMark className={className} />
		</Link>
	);
};
