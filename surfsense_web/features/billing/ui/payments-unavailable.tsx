"use client";

import { Wallet } from "lucide-react";
import { useTranslations } from "next-intl";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

/** Shown in place of pay buttons while no payment method is switched on. */
export function PaymentsUnavailable() {
	const t = useTranslations("billing");
	return (
		<Alert data-testid="billing-payments-unavailable">
			<Wallet aria-hidden />
			<AlertTitle>{t("payments_unavailable_title")}</AlertTitle>
			<AlertDescription>{t("payments_unavailable_body")}</AlertDescription>
		</Alert>
	);
}
