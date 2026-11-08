"use client";

import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import type { BillingMe } from "../api/billing-schemas";
import { useBillingActions } from "../hooks/use-billing-actions";
import { formatDate } from "../lib/format";

/** Stripe portal plus cancel/resume at period end, for a card subscription. */
export function SubscriptionActions({ me }: { me: BillingMe }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const actions = useBillingActions();
	const [confirming, setConfirming] = useState(false);
	const cancelling = !me.account.cancel_at_period_end;
	const showPortalBusy = useDelayedFlag(actions.portal.isPending);
	const showCancelBusy = useDelayedFlag(actions.cancel.isPending);
	if (!me.account.has_subscription || !me.payments.card) return null;
	const end = formatDate(me.account.period_end, locale) || "—";

	return (
		<div className="flex flex-wrap gap-2">
			<Button
				variant="outline"
				onClick={() => actions.portal.mutate()}
				disabled={actions.portal.isPending}
			>
				{showPortalBusy ? <Spinner size="sm" /> : null}
				{t("manage_subscription")}
			</Button>
			<Button variant={cancelling ? "ghost" : "outline"} onClick={() => setConfirming(true)}>
				{cancelling ? t("cancel_subscription") : t("resume_subscription")}
			</Button>

			<Dialog open={confirming} onOpenChange={setConfirming}>
				<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
					<DialogHeader>
						<DialogTitle>
							{cancelling ? t("cancel_dialog.title") : t("resume_dialog.title")}
						</DialogTitle>
						<DialogDescription>
							{cancelling
								? t("cancel_dialog.body", { date: end })
								: t("resume_dialog.body", { date: end })}
						</DialogDescription>
					</DialogHeader>
					<DialogFooter>
						<Button variant="ghost" onClick={() => setConfirming(false)}>
							{t("keep_as_is")}
						</Button>
						<Button
							variant={cancelling ? "destructive" : "default"}
							disabled={actions.cancel.isPending}
							onClick={() =>
								actions.cancel.mutate(cancelling, { onSuccess: () => setConfirming(false) })
							}
						>
							{showCancelBusy ? <Spinner size="sm" /> : null}
							{cancelling ? t("cancel_dialog.confirm") : t("resume_dialog.confirm")}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}
