"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import { AlertCircle, Info } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { myAccessAtom } from "@/atoms/members/members-query.atoms";
import { publicChatSnapshotsAtom } from "@/atoms/public-chat-snapshots/public-chat-snapshots-query.atoms";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import type { PublicChatSnapshotDetail } from "@/contracts/types/chat-threads.types";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { publicLinksApi } from "@/features/public-sharing";
import {
	canRevokePublicLink,
	canViewPublicLinks,
} from "@/features/public-sharing/lib/public-link-access";
import type { LiveLink } from "@/features/public-sharing/model/live-link";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { LiveNotebookLinkRow } from "./live-notebook-link-row";
import { PublicChatSnapshotRow } from "./public-chat-snapshot-row";

type Row =
	| { kind: "snapshot"; created_at: string; snapshot: PublicChatSnapshotDetail }
	| { kind: "notebook"; created_at: string; link: LiveLink };

interface PublicChatSnapshotsManagerProps {
	workspaceId: number;
}

/** Workspace settings › Public links: every read-only link, with copy and revoke. */
export function PublicChatSnapshotsManager({ workspaceId }: PublicChatSnapshotsManagerProps) {
	const t = useTranslations("publicSharing.links");
	const queryClient = useQueryClient();
	const { data: snapshotsData, isLoading, isError } = useAtomValue(publicChatSnapshotsAtom);
	const { data: access } = useAtomValue(myAccessAtom);
	const { data: me } = useAtomValue(currentUserAtom);
	const showSkeleton = useDelayedFlag(isLoading);
	const [revokeError, setRevokeError] = useState<string | null>(null);
	const liveLinks = useQuery({
		queryKey: cacheKeys.liveNotebookLinks.byWorkspace(workspaceId),
		queryFn: () => publicLinksApi.notebookLinks(workspaceId),
		enabled: workspaceId > 0 && canViewPublicLinks(access),
		meta: { suppressGlobalErrorToast: true },
	});
	const revokeLive = useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (link: LiveLink) => publicLinksApi.revokeNotebookLink(workspaceId, link.id),
		onSuccess: () => {
			setRevokeError(null);
			void queryClient.invalidateQueries({ queryKey: cacheKeys.liveNotebookLinks.all });
		},
		onError: () => setRevokeError(t("revoke_failed")),
	});

	const revoke = useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (snapshot: PublicChatSnapshotDetail) =>
			publicLinksApi.revoke(workspaceId, snapshot.id),
		onSuccess: () => {
			setRevokeError(null);
			void queryClient.invalidateQueries({ queryKey: cacheKeys.publicChatSnapshots.all });
		},
		onError: () => setRevokeError(t("revoke_failed")),
	});

	const intro = (
		<Alert>
			<Info />
			<AlertDescription>{t("intro")}</AlertDescription>
		</Alert>
	);

	if (isLoading || liveLinks.isLoading) {
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

	if (isError) {
		return (
			<Alert variant="destructive">
				<AlertCircle className="h-4 w-4" />
				<AlertDescription>{t("load_failed")}</AlertDescription>
			</Alert>
		);
	}

	const rows: Row[] = [
		...(liveLinks.data ?? []).map(
			(link): Row => ({ kind: "notebook", created_at: link.created_at, link })
		),
		...(snapshotsData?.snapshots ?? []).map(
			(snapshot): Row => ({ kind: "snapshot", created_at: snapshot.created_at, snapshot })
		),
	].sort((a, b) => b.created_at.localeCompare(a.created_at));

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
					{rows.map((row) =>
						row.kind === "notebook" ? (
							<LiveNotebookLinkRow
								key={`notebook-${row.link.id}`}
								link={row.link}
								revoking={revokeLive.isPending && revokeLive.variables?.id === row.link.id}
								onRevoke={async (link) => {
									await revokeLive.mutateAsync(link).catch(() => undefined);
								}}
							/>
						) : (
							<PublicChatSnapshotRow
								key={`snapshot-${row.snapshot.id}`}
								snapshot={row.snapshot}
								canRevoke={canRevokePublicLink(access, me?.id, row.snapshot.created_by_user_id)}
								revoking={revoke.isPending && revoke.variables?.id === row.snapshot.id}
								onRevoke={async (snapshot) => {
									await revoke.mutateAsync(snapshot).catch(() => undefined);
								}}
							/>
						)
					)}
				</ul>
			)}
		</div>
	);
}
