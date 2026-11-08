"use client";

import { useLocale, useTranslations } from "next-intl";

/** "Shared read-only · expires 2 October 2026", quietly under the title. */
export function SharedNotice({
	expiresAt,
	className,
}: {
	expiresAt: string | null | undefined;
	className?: string;
}) {
	const t = useTranslations("publicSharing");
	const locale = useLocale();
	const expiry = expiresAt
		? t("expires_on", {
				date: new Date(expiresAt).toLocaleDateString(locale, {
					year: "numeric",
					month: "long",
					day: "numeric",
				}),
			})
		: null;
	return (
		<p className={className ?? "text-xs text-muted-foreground"} data-testid="shared-notice">
			{t("read_only")}
			{expiry ? ` · ${expiry}` : null}
		</p>
	);
}
