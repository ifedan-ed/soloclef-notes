"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useAdminOverview } from "../../hooks/use-billing-admin";
import { useBillingMe } from "../../hooks/use-billing-queries";
import { AdminModelsTab } from "./admin-models-tab";
import { AdminPacksTab } from "./admin-packs-tab";
import { AdminPlansTab } from "./admin-plans-tab";
import { AdminPricesTab } from "./admin-prices-tab";
import { AdminReportTab } from "./admin-report-tab";
import { AdminSettingsTab } from "./admin-settings-tab";
import { AdminUsersTab } from "./admin-users-tab";

const TABS = ["settings", "plans", "prices", "packs", "models", "users", "report"] as const;
type AdminTab = (typeof TABS)[number];

/** Billing administration: modes and payment keys, the catalog, people and usage. */
export function BillingAdminPage() {
	const t = useTranslations("billing");
	const me = useBillingMe();
	const isAdmin = me.data?.is_admin === true;
	// Only admins ever ask for the overview.
	const overview = useAdminOverview(isAdmin);
	const [tab, setTab] = useState<AdminTab>("settings");
	const showSkeleton = useDelayedFlag(me.isLoading || (isAdmin && overview.isLoading));

	if (me.data && !isAdmin) {
		return <p className="text-sm text-muted-foreground">{t("admin.not_allowed")}</p>;
	}
	if (overview.isError) {
		return <p className="text-sm text-destructive">{t("errors.load_failed")}</p>;
	}
	if (!overview.data) {
		return showSkeleton ? <Skeleton className="h-64 w-full" /> : null;
	}

	return (
		<div className="min-w-0 space-y-4">
			<div>
				<h2 className="text-base font-semibold">{t("admin.title")}</h2>
				<p className="text-sm text-muted-foreground">{t("admin.description")}</p>
			</div>
			<Tabs value={tab} onValueChange={(value) => setTab(value as AdminTab)}>
				<div className="-mx-1 overflow-x-auto px-1">
					<TabsList className="w-max">
						{TABS.map((value) => (
							<TabsTrigger key={value} value={value}>
								{t(`admin.tabs.${value}`)}
							</TabsTrigger>
						))}
					</TabsList>
				</div>
				<TabsContent value="settings" className="pt-2">
					<AdminSettingsTab overview={overview.data} />
				</TabsContent>
				<TabsContent value="plans" className="pt-2">
					<AdminPlansTab overview={overview.data} />
				</TabsContent>
				<TabsContent value="prices" className="pt-2">
					<AdminPricesTab overview={overview.data} />
				</TabsContent>
				<TabsContent value="packs" className="pt-2">
					<AdminPacksTab overview={overview.data} />
				</TabsContent>
				<TabsContent value="models" className="pt-2">
					<AdminModelsTab />
				</TabsContent>
				<TabsContent value="users" className="pt-2">
					<AdminUsersTab overview={overview.data} />
				</TabsContent>
				<TabsContent value="report" className="pt-2">
					<AdminReportTab overview={overview.data} />
				</TabsContent>
			</Tabs>
		</div>
	);
}
