/**
 * How a dashboard route is framed. Every page except a notebook's own sits in
 * the page panel, centred at a readable width; a notebook is full-screen
 * (focus mode) and runs its own chat, so it scrolls itself.
 */
export interface PageFrame {
	focusMode: boolean;
	viewportClassName?: string;
	contentClassName?: string;
}

const VIEWPORT = "items-start justify-center px-6 py-8 md:px-10 md:pb-10 md:pt-16";

// Grids and tables read better with more room than a settings form.
const WIDE_PAGES = [/\/notebooks$/, /\/logs(\/|$)/];

// The Notes place fills the panel edge to edge: a list and a note side by
// side, each scrolling on its own, and text that can be selected.
const NOTES_FRAME: PageFrame = {
	focusMode: false,
	viewportClassName: "items-stretch justify-stretch overflow-hidden p-0",
	contentClassName: "h-full max-w-none",
};

export function pageFrame(pathname: string | null | undefined): PageFrame {
	const path = (pathname ?? "").replace(/\/+$/, "");
	if (/\/notebooks\/\d+$/.test(path)) return { focusMode: true };
	if (/\/notes(\/\d+)?$/.test(path)) return NOTES_FRAME;
	const wide = WIDE_PAGES.some((pattern) => pattern.test(path));
	return {
		focusMode: false,
		viewportClassName: VIEWPORT,
		contentClassName: wide ? "max-w-6xl select-none" : "max-w-5xl select-none",
	};
}
