/**
 * How a dashboard route is framed. Every page except a notebook's own sits in
 * the workspace panel, centred at a readable width; a notebook is full-screen
 * (focus mode) and runs its own chat, so it scrolls itself.
 */
export interface WorkspacePageFrame {
	focusMode: boolean;
	viewportClassName?: string;
	contentClassName?: string;
}

const VIEWPORT = "items-start justify-center px-6 py-8 md:px-10 md:pb-10 md:pt-16";

// Grids and tables read better with more room than a settings form.
const WIDE_PAGES = [/\/notebooks$/, /\/logs(\/|$)/];

export function workspacePageFrame(pathname: string | null | undefined): WorkspacePageFrame {
	const path = (pathname ?? "").replace(/\/+$/, "");
	if (/\/notebooks\/\d+$/.test(path)) return { focusMode: true };
	const wide = WIDE_PAGES.some((pattern) => pattern.test(path));
	return {
		focusMode: false,
		viewportClassName: VIEWPORT,
		contentClassName: wide ? "max-w-6xl select-none" : "max-w-5xl select-none",
	};
}
