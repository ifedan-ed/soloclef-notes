"use client";

import { usePathname } from "next/navigation";
import { SiteFooter } from "@/components/site/site-footer";
import { SiteNav } from "@/components/site/site-nav";

const AUTH_PAGES = new Set(["/login", "/register", "/forgot-password", "/reset-password"]);

/**
 * The chrome around every `(home)` route: one navigation, one ruled column,
 * one footer. The client half of the layout, because only the auth-page check
 * needs `usePathname`.
 */
export function SiteShell({ children }: { children: React.ReactNode }) {
	const pathname = usePathname();
	const isAuthPage = AUTH_PAGES.has(pathname);

	return (
		<main className="ss-home min-h-screen">
			<SiteNav />
			<div className="ss-home-shell">
				{children}
				{!isAuthPage ? <SiteFooter /> : null}
			</div>
		</main>
	);
}
