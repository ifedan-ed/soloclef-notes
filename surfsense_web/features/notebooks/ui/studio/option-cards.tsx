"use client";

import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

export interface OptionCard {
	value: string;
	label: string;
	description: string;
	preview?: string;
}

/** Large, described choices for the option that most shapes the output. */
export function OptionCards({
	value,
	options,
	onChange,
	label,
}: {
	value: string;
	options: OptionCard[];
	onChange: (value: string) => void;
	label: string;
}) {
	return (
		<fieldset aria-label={label} className="grid gap-2 sm:grid-cols-2">
			{options.map((option) => {
				const selected = option.value === value;
				return (
					<button
						key={option.value}
						type="button"
						aria-pressed={selected}
						onClick={() => onChange(option.value)}
						className={cn(
							"relative flex min-h-20 flex-col gap-1 rounded-xl border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
							selected ? "border-primary bg-primary/5" : "hover:bg-accent"
						)}
					>
						{option.preview ? (
							// biome-ignore lint/performance/noImgElement: a static SVG from /public needs no optimisation.
							<img
								src={option.preview}
								alt=""
								aria-hidden
								className="mb-1 aspect-[16/9] w-full rounded-lg border bg-muted object-cover"
							/>
						) : null}
						<span className="pr-6 text-sm font-medium text-foreground">{option.label}</span>
						<span className="text-xs leading-4 text-muted-foreground">{option.description}</span>
						{selected ? (
							<Check className="absolute right-3 top-3 size-4 text-primary" aria-hidden />
						) : null}
					</button>
				);
			})}
		</fieldset>
	);
}
