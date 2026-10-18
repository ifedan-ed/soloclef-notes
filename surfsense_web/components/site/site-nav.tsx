"use client";

import { IconMenu2, IconX } from "@tabler/icons-react";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { BrandMark } from "@/components/Logo";
import { NAV_LINKS } from "@/components/site/site-content";
import { BRAND } from "@/lib/brand";

/**
 * Site navigation.
 *
 * Rendered once by `app/(home)/layout.tsx`, above any route branch, so React
 * keeps it mounted across navigations and only the content below it changes.
 * Sticky and identical at every scroll position.
 */

function Wordmark() {
	return (
		<Link
			href="/"
			className="flex shrink-0 items-center gap-2 rounded-[2px] px-1 py-1 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-[color:var(--ring)]"
		>
			<BrandMark className="size-6" />
			<span className="text-[0.9375rem] font-semibold tracking-tight text-[color:var(--foreground)]">
				{BRAND.name}
			</span>
		</Link>
	);
}

export function SiteNav() {
	const [menuOpen, setMenuOpen] = useState(false);
	const headerRef = useRef<HTMLElement>(null);

	useEffect(() => {
		if (!menuOpen) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") setMenuOpen(false);
		};
		document.addEventListener("keydown", onKeyDown);
		return () => document.removeEventListener("keydown", onKeyDown);
	}, [menuOpen]);

	const close = () => setMenuOpen(false);

	return (
		<header ref={headerRef} className="ss-home-nav">
			<div className="ss-home-nav-bar">
				<Wordmark />

				<nav className="hidden items-center md:flex" aria-label="Main">
					{NAV_LINKS.map((link) => (
						<Link key={link.href} href={link.href} className="ss-home-nav-link">
							{link.name}
						</Link>
					))}
				</nav>

				<div className="flex items-center gap-1">
					<Link href="/login" className="ss-home-btn ss-home-btn-primary ss-home-btn-sm">
						Sign in
					</Link>
					<button
						type="button"
						onClick={() => setMenuOpen((open) => !open)}
						aria-label={menuOpen ? "Close menu" : "Open menu"}
						aria-expanded={menuOpen}
						className="ss-home-nav-link ss-home-nav-toggle"
					>
						{menuOpen ? (
							<IconX aria-hidden="true" className="size-4" />
						) : (
							<IconMenu2 aria-hidden="true" className="size-4" />
						)}
					</button>
				</div>
			</div>

			{menuOpen ? (
				<>
					<button
						type="button"
						aria-hidden="true"
						tabIndex={-1}
						className="ss-home-nav-scrim"
						onClick={close}
					/>
					<div className="ss-home-nav-drawer">
						<nav className="flex flex-col gap-0.5 px-4 py-3" aria-label="Menu">
							{NAV_LINKS.map((link) => (
								<Link key={link.href} href={link.href} onClick={close} className="ss-home-nav-link">
									{link.name}
								</Link>
							))}
						</nav>
					</div>
				</>
			) : null}
		</header>
	);
}
