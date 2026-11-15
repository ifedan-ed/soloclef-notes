"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Info } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { publicLinksApi } from "../api/public-links-api";
import type { PublicLink } from "../model/public-link";
import { PublicLinkRow } from "./public-link-row";

/** Settings › Public links: every shared Studio output of the account, with copy and revoke. */
export function PublicLinksManager() {
	const t = useTranslations("publicSharing.links");
	const queryClient = useQueryClient();
	const links = useQuery({
		queryKey: cacheKeys.publicLinks.list,
		queryFn: () => publicLinksApi.list(),
		staleTime: 5 * 60 * 1000,
	});
	const showSkeleton = useDelayedFlag(links.isLoading);
	const [revokeError, setRevokeError] = useState<string | null>(null);

	const revoke = useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (link: PublicLink) => publicLinksApi.revoke(link.id),
		onSuccess: () => {
			setRevokeError(null);
			void queryClient.invalidateQueries({ queryKey: cacheKeys.publicLinks.all });
		},
		onError: () => setRevokeError(t("revoke_failed")),
	});

	const intro = (
		<Alert>
			<Info />
			<AlertDescription>{t("intro")}</AlertDescription>
		</Alert>
	);

	if (links.isLoading) {
		return (
			<div className="space-y-4 md:space-y-5">
				{intro}
				{showSkeleton ? (
					<ul className="space-y-2">
						{["a", "b"].map((key) => (
							<li key={key} className="rounded-lg border p-4">
								<Skeleton className="h-4 w-40" />
								<Skeleton className="mt-2 h-3 w-64" />
							</li>
						))}
					</ul>
				) : null}
			</div>
		);
	}

	if (links.isError) {
		return (
			<Alert variant="destructive">
				<AlertCircle className="h-4 w-4" />
				<AlertDescription>{t("load_failed")}</AlertDescription>
			</Alert>
		);
	}

	const rows = links.data ?? [];

	return (
		<div className="space-y-4 md:space-y-5">
			{intro}
			{revokeError ? (
				<p role="alert" className="text-sm text-destructive">
					{revokeError}
				</p>
			) : null}
			{rows.length === 0 ? (
				<div className="flex flex-col items-center justify-center py-12 text-center">
					<h3 className="mb-2 text-sm font-semibold md:text-base">{t("empty_title")}</h3>
					<p className="max-w-sm text-xs text-muted-foreground">{t("empty_body")}</p>
				</div>
			) : (
				<ul className="space-y-2" data-testid="public-links">
					{rows.map((link) => (
						<PublicLinkRow
							key={link.id}
							link={link}
							revoking={revoke.isPending && revoke.variables?.id === link.id}
							onRevoke={async (target) => {
								await revoke.mutateAsync(target).catch(() => undefined);
							}}
						/>
					))}
				</ul>
			)}
		</div>
	);
}
