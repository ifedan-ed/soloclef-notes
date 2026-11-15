"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { usePushPreferences, useSavePushPreferences } from "../hooks/use-push-preferences";
import { PushKindList } from "./push-kind-list";
import { QuietHoursCard } from "./quiet-hours-card";

/**
 * Settings → Notifications: which kinds of push the phone app gets, and
 * quiet hours. Every change is saved as it is made. The inbox is not
 * filtered by any of this: it always keeps everything.
 */
export function NotificationSettings() {
	const t = useTranslations("notification_settings");
	const preferences = usePushPreferences();
	const save = useSavePushPreferences();

	if (preferences.isLoading) {
		return <NotificationSettingsSkeleton />;
	}

	if (preferences.isError || !preferences.data) {
		return (
			<div className="flex flex-col items-center gap-3 py-8 text-center">
				<p className="text-sm text-destructive">{t("load_failed")}</p>
				<Button variant="outline" size="sm" onClick={() => preferences.refetch()}>
					{t("retry")}
				</Button>
			</div>
		);
	}

	const change = save.mutate;
	return (
		<div className="space-y-8" data-testid="notification-settings">
			<p className="text-sm text-pretty text-muted-foreground">{t("intro")}</p>
			<PushKindList preferences={preferences.data} onChange={change} />
			<QuietHoursCard quietHours={preferences.data.quiet_hours} onChange={change} />
		</div>
	);
}

/** The page's shape while it loads: the intro line, a few switch rows, the quiet hours card. */
function NotificationSettingsSkeleton() {
	return (
		<div className="space-y-8" aria-busy>
			<Skeleton className="h-4 w-3/4" />
			<div className="space-y-3">
				<Skeleton className="h-5 w-40" />
				<div className="divide-y rounded-lg border">
					{["a", "b", "c", "d"].map((row) => (
						<div key={row} className="flex items-center justify-between gap-4 p-4">
							<div className="space-y-2">
								<Skeleton className="h-4 w-32" />
								<Skeleton className="h-3 w-56 max-w-full" />
							</div>
							<Skeleton className="h-6 w-11 shrink-0 rounded-full" />
						</div>
					))}
				</div>
			</div>
			<div className="flex items-center justify-between gap-4 rounded-lg border p-4">
				<div className="space-y-2">
					<Skeleton className="h-4 w-28" />
					<Skeleton className="h-3 w-64 max-w-full" />
				</div>
				<Skeleton className="h-6 w-11 shrink-0 rounded-full" />
			</div>
		</div>
	);
}
