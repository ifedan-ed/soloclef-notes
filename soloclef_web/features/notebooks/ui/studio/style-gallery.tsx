"use client";

import { Check, ChevronLeft, ChevronRight } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface GalleryOption {
	value: string;
	label: string;
	description: string;
	preview?: string;
}

/**
 * A horizontally scrolling row of picture cards, with arrows that appear only
 * when there is more to see in that direction.
 */
export function StyleGallery({
	value,
	options,
	onChange,
	label,
	scrollLabels,
	previewShape = "wide",
}: {
	value: string;
	options: GalleryOption[];
	onChange: (value: string) => void;
	label: string;
	scrollLabels: { previous: string; next: string };
	/** 16:9 previews, or square ones (NotebookLM's infographic styles). */
	previewShape?: "wide" | "square";
}) {
	const rowRef = useRef<HTMLFieldSetElement>(null);
	const [edges, setEdges] = useState({ start: true, end: false });

	const measure = useCallback(() => {
		const row = rowRef.current;
		if (!row) return;
		setEdges({
			start: row.scrollLeft <= 1,
			end: row.scrollLeft + row.clientWidth >= row.scrollWidth - 1,
		});
	}, []);

	useEffect(() => {
		measure();
		const row = rowRef.current;
		if (!row || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(measure);
		observer.observe(row);
		return () => observer.disconnect();
	}, [measure]);

	const scroll = (direction: 1 | -1) => {
		const row = rowRef.current;
		if (!row) return;
		const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
		row.scrollBy({ left: direction * row.clientWidth * 0.8, behavior: reduce ? "auto" : "smooth" });
	};

	// A fieldset's minimum width is its content's by default, so without
	// min-w-0 the row widened the whole dialog past the phone screen instead
	// of scrolling inside it.
	return (
		<div className="relative min-w-0">
			<fieldset
				ref={rowRef}
				aria-label={label}
				onScroll={measure}
				className="-mx-1 flex min-w-0 snap-x gap-2 overflow-x-auto px-1 pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
			>
				{options.map((option) => {
					const selected = option.value === value;
					return (
						<button
							key={option.value}
							type="button"
							aria-pressed={selected}
							title={option.description}
							onClick={() => onChange(option.value)}
							className={cn(
								"relative flex shrink-0 snap-start flex-col gap-1.5 rounded-xl border p-1.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
								previewShape === "square" ? "w-28" : "w-32",
								selected ? "border-primary bg-primary/5" : "hover:bg-accent"
							)}
						>
							{option.preview ? (
								// biome-ignore lint/performance/noImgElement: a small static preview from /public needs no optimisation.
								<img
									src={option.preview}
									alt=""
									aria-hidden
									className={cn(
										"w-full rounded-lg border bg-muted object-cover",
										previewShape === "square" ? "aspect-square" : "aspect-video"
									)}
								/>
							) : null}
							<span className="truncate px-0.5 text-xs font-medium text-foreground">
								{option.label}
							</span>
							{selected ? (
								<span className="absolute right-2.5 top-2.5 flex size-5 items-center justify-center rounded-full bg-primary text-primary-foreground">
									<Check className="size-3" aria-hidden />
								</span>
							) : null}
						</button>
					);
				})}
			</fieldset>
			{!edges.start ? (
				<Button
					type="button"
					variant="outline"
					size="icon"
					className="absolute left-0 top-[34%] size-8 -translate-x-1/3 rounded-full shadow-sm"
					onClick={() => scroll(-1)}
					aria-label={scrollLabels.previous}
				>
					<ChevronLeft className="size-4" />
				</Button>
			) : null}
			{!edges.end ? (
				<Button
					type="button"
					variant="outline"
					size="icon"
					className="absolute right-0 top-[34%] size-8 translate-x-1/3 rounded-full shadow-sm"
					onClick={() => scroll(1)}
					aria-label={scrollLabels.next}
				>
					<ChevronRight className="size-4" />
				</Button>
			) : null}
		</div>
	);
}
