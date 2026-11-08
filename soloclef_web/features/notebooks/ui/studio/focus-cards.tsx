"use client";

import { Check, PencilLine, Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Suggested topics as cards, plus a "Custom topic" card for writing one's own.
 * A suggestion is chosen when the focus is exactly it; anything else typed in
 * the focus box counts as the custom topic.
 */
export function FocusCards({
	suggestions,
	focus,
	onPick,
	onCustom,
	customLabel,
	customDescription,
	label,
}: {
	suggestions: readonly string[];
	focus: string;
	onPick: (suggestion: string) => void;
	onCustom: () => void;
	customLabel: string;
	customDescription: string;
	label: string;
}) {
	const chosen = suggestions.find((suggestion) => suggestion === focus.trim()) ?? null;
	const card =
		"relative flex min-h-16 items-start gap-2 rounded-xl border p-3 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";
	return (
		<fieldset aria-label={label} className="grid gap-2 sm:grid-cols-2">
			{suggestions.map((suggestion) => {
				const selected = suggestion === chosen;
				return (
					<button
						key={suggestion}
						type="button"
						aria-pressed={selected}
						onClick={() => onPick(suggestion)}
						className={cn(card, selected ? "border-primary bg-primary/5" : "hover:bg-accent")}
					>
						<Sparkles className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
						<span className="line-clamp-3 pr-5">{suggestion}</span>
						{selected ? (
							<Check className="absolute right-3 top-3 size-4 text-primary" aria-hidden />
						) : null}
					</button>
				);
			})}
			<button
				type="button"
				aria-pressed={chosen === null}
				onClick={onCustom}
				className={cn(card, chosen === null ? "border-primary bg-primary/5" : "hover:bg-accent")}
			>
				<PencilLine className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
				<span className="flex flex-col gap-0.5 pr-5">
					<span className="font-medium">{customLabel}</span>
					<span className="text-xs text-muted-foreground">{customDescription}</span>
				</span>
				{chosen === null ? (
					<Check className="absolute right-3 top-3 size-4 text-primary" aria-hidden />
				) : null}
			</button>
		</fieldset>
	);
}
