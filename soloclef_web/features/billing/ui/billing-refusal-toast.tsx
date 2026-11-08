"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import { createElement } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";

function RefusalTitle({ code, message }: { code?: string; message: string }) {
	const t = useTranslations("billing");
	const key = `refusal.${code}`;
	return <>{code && t.has(key) ? t(key) : message || t("refusal.GENERIC")}</>;
}

function BillingLink({ href }: { href: string }) {
	const t = useTranslations("billing");
	return (
		<Button asChild size="sm" variant="outline" className="ml-auto shrink-0">
			<Link href={href}>{t("refusal.action")}</Link>
		</Button>
	);
}

/**
 * A refusal from billing (402: out of credits, or not in the plan) as a toast
 * with a "Billing" button. Rendered as components so the text follows the UI
 * language even when raised from non-React code.
 */
export function showBillingRefusalToast({
	code,
	message,
	href,
}: {
	code?: string;
	message: string;
	href: string;
}) {
	toast.error(createElement(RefusalTitle, { code, message }), {
		action: createElement(BillingLink, { href }),
		duration: 10_000,
	});
}
