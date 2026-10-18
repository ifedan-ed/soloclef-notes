"use client";

import { Upload } from "lucide-react";
import { useTranslations } from "next-intl";

/** Covers the Sources panel while files are dragged over it. */
export function FileDropOverlay() {
	const t = useTranslations("notebook_research");
	return (
		<div className="pointer-events-none absolute inset-2 z-10 flex flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed border-primary bg-background/90 p-4 text-center">
			<Upload className="size-6 text-primary" aria-hidden />
			<p className="text-sm font-medium">{t("drop_overlay")}</p>
		</div>
	);
}
