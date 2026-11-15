import { z } from "zod";

/** The chat's figure tool, as the model calls it (soloclef_api, figures-go's `draw_figure`). */
export const DRAW_FIGURE_TOOL = "draw_figure";

/**
 * A drawn figure, as the server sends it for a finished `draw_figure` call.
 * `svg` is the figure for light pages and `svg_dark` the same figure for dark
 * ones: two complete SVG documents, sanitised on the server and each with its
 * own background, so the page never has to recolour them. The title and the
 * caption are drawn inside the SVG; they are here for the alt text and for
 * readers of the saved message, not to be shown again around the picture.
 */
const card = z.object({
	type: z.string().min(1),
	title: z.string(),
	alt: z.string().min(1),
	caption: z.string().optional(),
	width: z.number().positive(),
	height: z.number().positive(),
	svg: z.string().startsWith("<svg"),
	svg_dark: z.string().startsWith("<svg"),
});
export type FigureCardData = z.infer<typeof card>;

/** What the card draws: both versions as image URLs, and the box they fill. */
export interface FigureImage {
	alt: string;
	/** width / height, for a box that keeps its shape before the picture decodes. */
	aspectRatio: number;
	light: string;
	dark: string;
}

/**
 * An SVG document as an image URL. The figure is shown through `<img>`, never
 * put into the page as markup: as an image it cannot run script, reach the
 * page's styles, or collide with the page's ids, however it was written.
 * Percent-encoding (not base64) keeps the URL about as long as the SVG.
 */
export function svgDataUrl(svg: string): string {
	return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

// A tool result is a fixed object once the call has ended, and the same
// object is passed to every render of the message and to the trace grouping
// on every streamed token. Parsing and encoding it once per object keeps a
// long answer with several figures cheap to redraw. A WeakMap lets the entry
// go when the message does.
const parsed = new WeakMap<object, FigureCardData | null>();
const images = new WeakMap<FigureCardData, FigureImage>();

/**
 * The figure for a tool result, or null when it is not one: a call still
 * running (no result yet), a call the server refused (`{error}`, which the
 * model corrects and calls again, or answers in words), or a shape from
 * another version. Null leaves the call in the turn's trace as a step.
 */
export function parseFigureCard(result: unknown): FigureCardData | null {
	if (typeof result !== "object" || result === null) return null;
	const cached = parsed.get(result);
	if (cached !== undefined) return cached;
	const outcome = card.safeParse(result);
	const data = outcome.success ? outcome.data : null;
	parsed.set(result, data);
	return data;
}

/** The figure's two image URLs and its shape, made once per figure. */
export function figureImage(data: FigureCardData): FigureImage {
	const cached = images.get(data);
	if (cached) return cached;
	const image: FigureImage = {
		alt: data.alt,
		aspectRatio: data.width / data.height,
		light: svgDataUrl(data.svg),
		dark: svgDataUrl(data.svg_dark),
	};
	images.set(data, image);
	return image;
}
