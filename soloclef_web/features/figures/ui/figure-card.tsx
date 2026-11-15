"use client";

import type { ToolCallMessagePartProps } from "@assistant-ui/react";
import { useMemo } from "react";
import { type FigureImage, figureImage, parseFigureCard } from "../figure-card";

/**
 * A figure in the answer, drawn as PedsHub's AI Mode draws one: the full
 * width of the message column up to 560px, in a box of the figure's own shape
 * so the text below does not move while it decodes.
 *
 * Both versions are in the page and CSS shows the one for the page's theme
 * (the `dark` class next-themes sets on <html>). Choosing in CSS rather than
 * from the theme hook means no first paint in the wrong colours and no
 * hydration mismatch, and a hidden <img> is out of the accessibility tree, so
 * the alt text is read once.
 */
export function FigureCard({ image }: { image: FigureImage }) {
	return (
		<figure
			className="my-4 w-full max-w-[560px] overflow-hidden rounded-xl border"
			style={{ aspectRatio: image.aspectRatio }}
		>
			{/* biome-ignore lint/performance/noImgElement: an in-memory SVG; next/image adds nothing here */}
			<img src={image.light} alt={image.alt} className="block size-full dark:hidden" />
			{/* biome-ignore lint/performance/noImgElement: as above */}
			<img src={image.dark} alt={image.alt} className="hidden size-full dark:block" />
		</figure>
	);
}

/**
 * The `draw_figure` tool part. Only a finished, well-formed figure has a body
 * here; a call still running or refused is a step in the turn's trace (see
 * isBodyTool in features/chat-messages/timeline/grouping.ts), so this renders
 * nothing for it.
 */
export function DrawFigureToolUI({ result }: ToolCallMessagePartProps<unknown, unknown>) {
	// parseFigureCard and figureImage are cached per result object, so this
	// memo only saves the lookups; it also keeps `image` stable for React.
	const image = useMemo(() => {
		const data = parseFigureCard(result);
		return data ? figureImage(data) : null;
	}, [result]);
	return image ? <FigureCard image={image} /> : null;
}
