"use client";

import { useTranslations } from "next-intl";
import { useLayoutEffect, useRef } from "react";
import { cn } from "@/lib/utils";

/** The note's title as a heading that is also a field: it wraps and grows rather than scrolling sideways. */
export function NoteTitleField({
	value,
	onChange,
	onEnter,
	className,
}: {
	value: string;
	onChange: (value: string) => void;
	/** Enter moves on to the text, as it would in a document. */
	onEnter?: () => void;
	className?: string;
}) {
	const t = useTranslations("notes");
	const ref = useRef<HTMLTextAreaElement>(null);
	// The height follows the text, and the width: a notebook's panel widens
	// as a note opens in it, and a height measured mid-way kept a blank line.
	// biome-ignore lint/correctness/useExhaustiveDependencies: re-measured when the text changes
	useLayoutEffect(() => {
		const field = ref.current;
		if (!field) return;
		const fit = () => {
			field.style.height = "auto";
			field.style.height = `${field.scrollHeight}px`;
		};
		fit();
		// Only a change of width refits: the height this sets is a resize too.
		let width = field.clientWidth;
		const observer = new ResizeObserver(() => {
			if (field.clientWidth === width) return;
			width = field.clientWidth;
			fit();
		});
		observer.observe(field);
		return () => observer.disconnect();
	}, [value]);
	return (
		<textarea
			ref={ref}
			value={value}
			rows={1}
			maxLength={300}
			placeholder={t("untitled")}
			aria-label={t("note_title")}
			onChange={(event) => onChange(event.target.value.replace(/\n/g, " "))}
			onKeyDown={(event) => {
				if (event.key === "Enter") {
					event.preventDefault();
					onEnter?.();
				}
			}}
			className={cn(
				"w-full resize-none overflow-hidden border-0 bg-transparent p-0 font-semibold tracking-tight",
				"placeholder:text-muted-foreground/60 focus:outline-none focus-visible:ring-0",
				className
			)}
		/>
	);
}
