"use client";

import { useRouter } from "next/navigation";
import type { ReactNode } from "react";
import { DropdownMenuItem } from "@/components/ui/dropdown-menu";

/**
 * A source row that leads to connecting it. Its own component so the router
 * is asked for only while the menu is open and such a row is drawn.
 */
export function ConnectSourceItem({
	href,
	label,
	children,
}: {
	href: string;
	label: string;
	children: ReactNode;
}) {
	const router = useRouter();
	return (
		<DropdownMenuItem className="pl-8" aria-label={label} onSelect={() => router.push(href)}>
			{children}
		</DropdownMenuItem>
	);
}
