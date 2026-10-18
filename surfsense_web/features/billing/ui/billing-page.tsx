"use client";

import { usePathname, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useBillingMe } from "../hooks/use-billing-queries";
import { returnFlag } from "../lib/billing-errors";
import { BillingView } from "./billing-view";
import { PaymentsList } from "./payments-list";
import { PurchaseDialog, type PurchaseTarget } from "./purchase-dialog";
import { UsageHistory } from "./usage-history";

/**
 * Toasts once for a payment return (`?checkout=success|cancel` from Stripe,
 * `?crypto=done` from BTCPay), then drops the flag from the address.
 */
function useCheckoutToast() {
	const t = useTranslations("billing");
	const router = useRouter();
	const pathname = usePathname();
	const shown = useRef(false);
	useEffect(() => {
		const flag = returnFlag(window.location.search);
		if (!flag || shown.current) return;
		shown.current = true;
		if (flag === "success") toast.success(t("toasts.checkout_success"));
		else if (flag === "crypto_done") toast.success(t("toasts.crypto_done"));
		else toast.info(t("toasts.checkout_cancel"));
		router.replace(pathname, { scroll: false });
	}, [pathname, router, t]);
}

/** The Billing settings tab: loads the account once, then keeps it painted. */
export function BillingPage() {
	const t = useTranslations("billing");
	const me = useBillingMe();
	const [target, setTarget] = useState<PurchaseTarget | null>(null);
	const showSkeleton = useDelayedFlag(me.isLoading);
	useCheckoutToast();

	if (!me.data) {
		if (me.isError) {
			return (
				<div className="space-y-3">
					<p className="text-sm text-destructive">{t("errors.load_failed")}</p>
					<Button variant="outline" onClick={() => me.refetch()}>
						{t("retry")}
					</Button>
				</div>
			);
		}
		return showSkeleton ? (
			<div className="space-y-3" aria-busy>
				<div className="grid gap-3 sm:grid-cols-2">
					<Skeleton className="h-32 w-full" />
					<Skeleton className="h-32 w-full" />
				</div>
				<Skeleton className="h-48 w-full" />
			</div>
		) : null;
	}

	return (
		<div className="space-y-10 min-w-0">
			<BillingView
				me={me.data}
				onChoosePlan={(plan) => setTarget({ kind: "plan", plan })}
				onBuyPack={(pack) => setTarget({ kind: "pack", pack })}
			/>
			<UsageHistory prices={me.data.prices} />
			<PaymentsList />

			<PurchaseDialog
				target={target}
				me={me.data}
				onOpenChange={(open) => {
					if (!open) setTarget(null);
				}}
			/>
		</div>
	);
}
