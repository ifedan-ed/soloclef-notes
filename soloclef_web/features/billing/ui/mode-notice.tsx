"use client";

import { Info } from "lucide-react";
import { useTranslations } from "next-intl";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import type { BillingMode } from "../api/billing-schemas";

/** Says plainly whether anything is charged yet: shadow measures, off does nothing. */
export function ModeNotice({ mode }: { mode: BillingMode }) {
	const t = useTranslations("billing");
	if (mode === "live") return null;
	return (
		<Alert data-testid="billing-mode-notice">
			<Info aria-hidden />
			<AlertTitle>{t(`mode_notice.${mode}_title`)}</AlertTitle>
			<AlertDescription>{t(`mode_notice.${mode}_body`)}</AlertDescription>
		</Alert>
	);
}
