import Link from "next/link";
import { BrandMark } from "@/components/Logo";
import { FOOTER_COLUMNS } from "@/components/site/site-content";
import { BRAND } from "@/lib/brand";

/**
 * Site footer: identity and tagline on the left, link columns on the right.
 * It reads the page's palette, so it follows the visitor's light or dark theme.
 */
export function SiteFooter() {
	return (
		<footer className="ss-home-rule bg-[color:var(--app-shell)]">
			<div className="ss-home-pad flex flex-col gap-10 py-14 lg:flex-row lg:justify-between lg:gap-16">
				<div className="flex max-w-sm shrink-0 flex-col justify-between gap-8">
					<div>
						<Link href="/" className="inline-flex items-center gap-2">
							<BrandMark className="size-7" />
							<span className="text-lg font-semibold tracking-tight text-[color:var(--foreground)]">
								{BRAND.name}
							</span>
						</Link>
						<p className="ss-home-body mt-3 text-sm">{BRAND.tagline}</p>
					</div>
					<p className="text-xs text-[color:var(--muted-foreground)]">
						&copy; {new Date().getFullYear()} {BRAND.name}
					</p>
				</div>

				<div className="grid grid-cols-2 gap-x-8 gap-y-8 sm:grid-cols-3 lg:gap-x-16">
					{FOOTER_COLUMNS.map((column) => (
						<div key={column.heading}>
							<p className="text-sm font-semibold text-[color:var(--foreground)]">
								{column.heading}
							</p>
							<ul className="mt-3 flex list-none flex-col gap-2.5 p-0">
								{column.links.map((link) => (
									<li key={link.href}>
										<Link
											href={link.href}
											className="text-sm text-[color:var(--muted-foreground)] transition-colors duration-100 hover:text-[color:var(--foreground)]"
										>
											{link.name}
										</Link>
									</li>
								))}
							</ul>
						</div>
					))}
				</div>
			</div>
		</footer>
	);
}
