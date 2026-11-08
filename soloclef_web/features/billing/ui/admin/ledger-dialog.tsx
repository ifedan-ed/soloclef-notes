"use client";

import { useLocale, useTranslations } from "next-intl";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import type { AdminUser } from "../../api/billing-admin-schemas";
import { useAdminLedger } from "../../hooks/use-billing-admin";
import { formatCredits, formatDateTime } from "../../lib/format";

/** A person's credit ledger, newest first. */
export function LedgerDialog({ user, onClose }: { user: AdminUser | null; onClose: () => void }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const ledger = useAdminLedger(user?.user_id ?? null);
	const showSkeleton = useDelayedFlag(ledger.isLoading);
	return (
		<Dialog open={user !== null} onOpenChange={(open) => !open && onClose()}>
			<DialogContent className="flex max-h-[85vh] w-[calc(100vw-2rem)] max-w-lg flex-col gap-3 overflow-y-auto">
				<DialogHeader>
					<DialogTitle>{t("admin.ledger")}</DialogTitle>
					<DialogDescription className="break-all">{user?.email}</DialogDescription>
				</DialogHeader>
				{ledger.isError ? (
					<p className="text-sm text-destructive">{t("errors.load_failed")}</p>
				) : !ledger.data ? (
					showSkeleton ? (
						<Skeleton className="h-32 w-full" />
					) : null
				) : ledger.data.items.length === 0 ? (
					<p className="text-sm text-muted-foreground">{t("admin.ledger_empty")}</p>
				) : (
					<ul className="divide-y rounded-lg border text-sm">
						{ledger.data.items.map((entry) => (
							<li key={entry.id} className="space-y-0.5 p-2">
								<div className="flex justify-between gap-2">
									<span className="font-medium tabular-nums">
										{entry.credits > 0 ? "+" : ""}
										{formatCredits(entry.credits, locale)}
									</span>
									<span className="text-muted-foreground">
										{t.has(`admin.buckets.${entry.bucket}`)
											? t(`admin.buckets.${entry.bucket}`)
											: entry.bucket}{" "}
										· {formatCredits(entry.balance_after, locale)}
									</span>
								</div>
								<p className="text-xs text-muted-foreground">
									{formatDateTime(entry.created_at, locale)} · {entry.kind}
									{entry.reason ? ` · ${entry.reason}` : ""}
								</p>
							</li>
						))}
					</ul>
				)}
			</DialogContent>
		</Dialog>
	);
}
