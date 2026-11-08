import Link from "next/link";
import { BrandMark } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { BRAND } from "@/lib/brand";

/**
 * The bar above pages a signed-out visitor can open inside the app, such as a
 * shared chat. Fixed and 4rem tall: the page below pads its top by that much.
 */
export function PublicHeader() {
	return (
		<header className="fixed inset-x-0 top-0 z-40 flex h-16 items-center justify-between border-b border-border/40 bg-main-panel/80 px-4 backdrop-blur-md sm:px-6">
			<Link href="/" className="flex items-center gap-2 rounded-md">
				<BrandMark className="size-7" />
				<span className="text-base font-semibold tracking-tight">{BRAND.name}</span>
			</Link>
			<Button asChild size="sm">
				<Link href="/login">Sign in</Link>
			</Button>
		</header>
	);
}
