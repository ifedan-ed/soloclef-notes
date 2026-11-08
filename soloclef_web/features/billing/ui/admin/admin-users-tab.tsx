"use client";

import { Search } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import type { AdminOverview, AdminUser } from "../../api/billing-admin-schemas";
import { useAdminUsers } from "../../hooks/use-billing-admin";
import { formatCredits } from "../../lib/format";
import { AdjustCreditsDialog } from "./adjust-credits-dialog";
import { LedgerDialog } from "./ledger-dialog";
import { UserAccountDialog } from "./user-account-dialog";

const PAGE = 25;

function useDebounced<T>(value: T, ms = 300): T {
	const [debounced, setDebounced] = useState(value);
	useEffect(() => {
		const timer = window.setTimeout(() => setDebounced(value), ms);
		return () => window.clearTimeout(timer);
	}, [value, ms]);
	return debounced;
}

/** Find a person, then set their plan, exempt them, adjust credits or read their ledger. */
export function AdminUsersTab({ overview }: { overview: AdminOverview }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const [search, setSearch] = useState("");
	const q = useDebounced(search.trim());
	const [offset, setOffset] = useState(0);
	const users = useAdminUsers(q, offset, PAGE);
	const showSkeleton = useDelayedFlag(users.isLoading);
	const [managing, setManaging] = useState<AdminUser | null>(null);
	const [adjusting, setAdjusting] = useState<AdminUser | null>(null);
	const [ledgerFor, setLedgerFor] = useState<AdminUser | null>(null);
	const planName = (id: number | null | undefined) =>
		overview.plans.find((p) => p.id === id)?.name ?? "—";
	const items = users.data?.items ?? [];

	return (
		<div className="space-y-3">
			<div className="relative">
				<Search
					className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
					aria-hidden
				/>
				<Input
					type="search"
					className="pl-9"
					placeholder={t("admin.search_users")}
					aria-label={t("admin.search_users")}
					value={search}
					onChange={(e) => {
						setSearch(e.target.value);
						setOffset(0);
					}}
				/>
			</div>

			{users.isError ? (
				<p className="text-sm text-destructive">{t("errors.load_failed")}</p>
			) : !users.data ? (
				showSkeleton ? (
					<Skeleton className="h-40 w-full" />
				) : null
			) : items.length === 0 ? (
				<p className="text-sm text-muted-foreground">{t("admin.no_users")}</p>
			) : (
				<ul className="divide-y rounded-lg border">
					{items.map((user) => (
						<li key={user.user_id} className="space-y-2 p-3">
							<div className="flex flex-wrap items-center gap-2">
								<span className="min-w-0 break-all font-medium">{user.email}</span>
								{user.account?.exempt || user.is_config_admin ? (
									<Badge variant="secondary">{t("exempt_badge")}</Badge>
								) : null}
							</div>
							<p className="text-sm text-muted-foreground">
								{t("admin.user_line", {
									plan: planName(user.account?.plan_id),
									plan_credits: formatCredits(user.plan_credits ?? 0, locale),
									topup: formatCredits(user.topup_credits, locale),
								})}
							</p>
							<div className="flex flex-wrap gap-2">
								<Button size="sm" variant="outline" onClick={() => setManaging(user)}>
									{t("admin.manage")}
								</Button>
								<Button size="sm" variant="outline" onClick={() => setAdjusting(user)}>
									{t("admin.adjust_credits")}
								</Button>
								<Button size="sm" variant="ghost" onClick={() => setLedgerFor(user)}>
									{t("admin.view_ledger")}
								</Button>
							</div>
						</li>
					))}
				</ul>
			)}

			{users.data && (offset > 0 || items.length === PAGE) ? (
				<div className="flex justify-between gap-2">
					<Button
						size="sm"
						variant="outline"
						disabled={offset === 0}
						onClick={() => setOffset((o) => Math.max(0, o - PAGE))}
					>
						{t("usage.previous")}
					</Button>
					<Button
						size="sm"
						variant="outline"
						disabled={items.length < PAGE}
						onClick={() => setOffset((o) => o + PAGE)}
					>
						{t("usage.next")}
					</Button>
				</div>
			) : null}

			<UserAccountDialog user={managing} plans={overview.plans} onClose={() => setManaging(null)} />
			<AdjustCreditsDialog user={adjusting} onClose={() => setAdjusting(null)} />
			<LedgerDialog user={ledgerFor} onClose={() => setLedgerFor(null)} />
		</div>
	);
}
