"use client";

import * as RadioGroupPrimitive from "@radix-ui/react-radio-group";
import { Cloud, HardDrive } from "lucide-react";
import { useTranslations } from "next-intl";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import type { DocumentParser } from "@/contracts/types/document.types";
import type { ParserChoice } from "@/lib/etl/parser-choice";
import { cn } from "@/lib/utils";
import { PARSER_DESCRIPTION_KEYS, PARSER_NAME_KEYS, unavailableReasonKey } from "../parser-labels";

interface ParserPickerProps {
	/** What the server offers and which parser would be sent (resolveParserChoice). */
	choice: ParserChoice;
	onChange: (parser: DocumentParser) => void;
	/**
	 * "loading" and "failed" are shown in words, because a source cannot be
	 * added without a parser and a picker that is merely absent would leave
	 * a disabled Add button unexplained.
	 */
	status: "loading" | "failed" | "ready";
	disabled?: boolean;
	/**
	 * "cards" where there is room to say what each parser does (the upload
	 * dialog); "select" where the picker shares a row with other controls
	 * (the Nextcloud picker, a connection's settings).
	 */
	variant: "cards" | "select";
	/**
	 * Replaces the line shown when nothing is picked. A connection's settings
	 * use it: there "nothing picked" means its owner has not chosen yet, not
	 * that the server's default is unavailable.
	 */
	emptyHint?: string;
	className?: string;
}

/**
 * The one parser picker: an upload, a notebook's added sources, a Nextcloud
 * import and a connection all choose among the same four parsers, so they
 * share this instead of each keeping its own copy of the list.
 *
 * All four parsers are always listed. One the server has no key for is
 * shown as unavailable, with the reason, and cannot be picked; it is not
 * hidden, so a person can see what an administrator could switch on.
 */
export function ParserPicker({
	choice,
	onChange,
	status,
	disabled = false,
	variant,
	emptyHint,
	className,
}: ParserPickerProps) {
	const t = useTranslations("upload_documents");

	if (status !== "ready" || choice.options.length === 0) {
		return (
			<p
				className={cn(
					"text-xs",
					status === "failed" ? "text-destructive" : "text-muted-foreground",
					className
				)}
				role={status === "failed" ? "alert" : "status"}
			>
				{status === "failed" ? t("parser_load_failed") : t("parser_loading")}
			</p>
		);
	}

	// Nothing to send although the choices are here: the server's default is
	// not configured. Say which of the two situations it is; never pick one
	// for the person.
	const nothingPicked = choice.effectiveParser === undefined;
	const anyAvailable = choice.options.some((option) => option.available);
	const prompt = nothingPicked ? (
		<output className="block text-xs text-amber-700 dark:text-amber-400">
			{!anyAvailable ? t("parser_none_configured") : (emptyHint ?? t("parser_pick_available"))}
		</output>
	) : null;

	if (variant === "select") {
		return (
			<div className={cn("space-y-1.5", className)}>
				<Select
					// Radix shows the placeholder only for an empty string.
					value={choice.effectiveParser ?? ""}
					onValueChange={(value) => onChange(value as DocumentParser)}
					disabled={disabled}
				>
					<SelectTrigger aria-label={t("parser_label")} className="h-9 w-full sm:w-56">
						{/* The name is given outright, not left for the list to supply:
						    the list is only mounted once opened, and the closed
						    control would otherwise be blank until then. */}
						<SelectValue placeholder={t("parser_label")}>
							{choice.effectiveParser ? t(PARSER_NAME_KEYS[choice.effectiveParser]) : null}
						</SelectValue>
					</SelectTrigger>
					<SelectContent>
						{choice.options.map((option) => (
							<SelectItem
								key={option.parser}
								value={option.parser}
								disabled={!option.available}
								description={
									option.available ? undefined : t(unavailableReasonKey(option.unavailableReason))
								}
							>
								{t(PARSER_NAME_KEYS[option.parser])}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				{prompt}
			</div>
		);
	}

	return (
		<div className={cn("space-y-1.5", className)}>
			<RadioGroupPrimitive.Root
				aria-label={t("parser_label")}
				value={choice.effectiveParser ?? ""}
				onValueChange={(value) => onChange(value as DocumentParser)}
				disabled={disabled}
				// One column on a phone: four cards two abreast left each
				// description three words wide.
				className="grid grid-cols-1 gap-2 sm:grid-cols-2"
			>
				{choice.options.map((option) => {
					const selected = choice.effectiveParser === option.parser;
					const Icon = option.parser === "local" ? HardDrive : Cloud;
					return (
						<RadioGroupPrimitive.Item
							key={option.parser}
							value={option.parser}
							disabled={!option.available}
							data-testid={`parser-option-${option.parser}`}
							className={cn(
								"flex w-full items-start gap-2.5 rounded-lg border p-3 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/50",
								selected
									? "border-primary bg-primary/5"
									: // Stronger than the theme's hairline border: inside a dialog
										// that hairline vanishes, and a card with no edge does not
										// read as something to press.
										"border-muted-foreground/25 hover:border-muted-foreground/60",
								// Dashed and dimmed, not merely greyed: it has to read as
								// "not offered here" at a glance, beside three that are.
								!option.available &&
									"cursor-not-allowed border-dashed bg-muted/30 hover:border-muted-foreground/25",
								disabled && "cursor-not-allowed opacity-60"
							)}
						>
							<Icon
								aria-hidden
								className={cn(
									"mt-0.5 size-4 shrink-0",
									selected ? "text-primary" : "text-muted-foreground",
									!option.available && "opacity-50"
								)}
							/>
							<span className="min-w-0 space-y-0.5">
								<span
									className={cn(
										"block text-sm font-medium",
										!option.available && "text-muted-foreground"
									)}
								>
									{t(PARSER_NAME_KEYS[option.parser])}
								</span>
								<span className="block text-xs text-muted-foreground">
									{option.available
										? t(PARSER_DESCRIPTION_KEYS[option.parser])
										: t(unavailableReasonKey(option.unavailableReason))}
								</span>
							</span>
						</RadioGroupPrimitive.Item>
					);
				})}
			</RadioGroupPrimitive.Root>
			{prompt}
		</div>
	);
}
