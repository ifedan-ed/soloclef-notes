"use client";

import { useTranslations } from "next-intl";

/** "See plans and pricing", for links to /pricing from the marketing pages. */
export function SeePlansLabel() {
	const t = useTranslations("billing");
	return <>{t("pricing_page.see_plans")}</>;
}
