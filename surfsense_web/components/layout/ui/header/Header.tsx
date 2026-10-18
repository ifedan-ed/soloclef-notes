"use client";

interface HeaderProps {
	mobileMenuTrigger?: React.ReactNode;
}

/**
 * The top bar of the mobile shell and of the anonymous chat: only the menu
 * trigger. The chat actions it used to carry (share, action log, artifacts)
 * belonged to the retired standalone chat page; a notebook has its own header.
 */
export function Header({ mobileMenuTrigger }: HeaderProps) {
	if (!mobileMenuTrigger) return null;
	return (
		<header className="sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 bg-main-panel/95 backdrop-blur supports-backdrop-filter:bg-main-panel/60 px-4">
			<div className="flex flex-1 items-center gap-2 min-w-0">{mobileMenuTrigger}</div>
		</header>
	);
}
