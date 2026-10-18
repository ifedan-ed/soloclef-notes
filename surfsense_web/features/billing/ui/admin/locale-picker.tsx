"use client";

import { useLocale, useTranslations } from "next-intl";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { CONTENT_LOCALES, type ContentLocale } from "../../lib/admin-forms";

function languageName(code: string, displayLocale: string): string {
	try {
		return new Intl.DisplayNames([displayLocale], { type: "language" }).of(code) ?? code;
	} catch {
		return code;
	}
}

/** Which language's content is being edited. English is required; others fall back to it. */
export function LocalePicker({
	value,
	onChange,
}: {
	value: ContentLocale;
	onChange: (locale: ContentLocale) => void;
}) {
	const t = useTranslations("billing");
	const uiLocale = useLocale();
	return (
		<div className="space-y-1">
			<SegmentedControl
				ariaLabel={t("admin.content_language")}
				value={value}
				onValueChange={(v) => onChange(v as ContentLocale)}
				options={CONTENT_LOCALES.map((code) => ({
					value: code,
					label: code.toUpperCase(),
					ariaLabel: languageName(code, uiLocale),
				}))}
			/>
			<p className="text-xs text-muted-foreground">
				{value === "en" ? t("admin.content_en_required") : t("admin.content_fallback")}
			</p>
		</div>
	);
}
