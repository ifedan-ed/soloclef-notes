"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import { useRouter } from "next/navigation";
import { useLocale } from "next-intl";
import { useEffect, useRef } from "react";
import { workspacesAtom } from "@/atoms/workspaces/workspace-query.atoms";
import { useGlobalLoadingEffect } from "@/hooks/use-global-loading";
import { billingApi } from "../api/billing-api";
import { billingKeys } from "../api/billing-query-keys";
import { billingReturnTarget, returnFlag } from "../lib/billing-errors";

/**
 * `/billing`: where Stripe Checkout, BTCPay's checkout (`?crypto=done`) and
 * the apps send people. Settles a finished Stripe checkout at once (the webhook does too; both are idempotent), then
 * moves on to the Billing tab of the first workspace, the same one the
 * dashboard opens, keeping the success/cancel flag for a toast there.
 */
export function BillingReturn() {
	const router = useRouter();
	const locale = useLocale();
	const queryClient = useQueryClient();
	const { data: workspaces = [], isLoading, error } = useAtomValue(workspacesAtom);
	const started = useRef(false);
	useGlobalLoadingEffect(true);

	useEffect(() => {
		if (isLoading || started.current) return;
		started.current = true;
		const params = new URLSearchParams(window.location.search);
		const flag = returnFlag(params);
		const sessionId = params.get("session_id");
		const settle =
			flag === "success" && sessionId
				? billingApi.stripeReturn(locale, sessionId).catch(() => null)
				: Promise.resolve(null);
		void settle.then(() => {
			void queryClient.invalidateQueries({ queryKey: billingKeys.all });
			const first = workspaces[0];
			router.replace(first && !error ? billingReturnTarget(first.id, flag) : "/dashboard");
		});
	}, [isLoading, workspaces, error, locale, router, queryClient]);

	return null;
}
