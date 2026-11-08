"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Check, ChevronRight, Copy, Link2, Mail, RotateCw, Trash2 } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { Invite } from "@/contracts/types/invites.types";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { invitesApiService } from "@/lib/apis/invites-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { cn, copyToClipboard } from "@/lib/utils";
import { apiErrorDetail } from "../lib/api-error-detail";
import { inviteLink } from "../lib/invite-link";
import { expiredInvites, pendingInvites } from "../lib/pending-invites";

/** Invitations still open, on the Team page itself, with resend, copy and revoke. */
export function PendingInvites({
	workspaceId,
	invites,
}: {
	workspaceId: number;
	invites: Invite[];
}) {
	const t = useTranslations("team");
	const pending = pendingInvites(invites);
	const expired = expiredInvites(invites);
	const [showExpired, setShowExpired] = useState(false);

	return (
		<section className="space-y-3" aria-labelledby="pending-invites-title">
			<h2 id="pending-invites-title" className="text-base font-semibold">
				{t("pending_title")}
			</h2>
			{pending.length === 0 ? (
				<p className="rounded-lg border border-dashed border-border/60 px-4 py-6 text-center text-sm text-muted-foreground">
					{t("pending_empty")}
				</p>
			) : (
				<ul className="divide-y divide-border/60 overflow-hidden rounded-lg border border-border/60">
					{pending.map((invite) => (
						<InviteRow key={invite.id} workspaceId={workspaceId} invite={invite} />
					))}
				</ul>
			)}
			{expired.length > 0 && (
				<div>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="gap-1 px-1 text-muted-foreground"
						aria-expanded={showExpired}
						onClick={() => setShowExpired((v) => !v)}
					>
						<ChevronRight
							className={cn("size-4 transition-transform", showExpired && "rotate-90")}
						/>
						{t("expired_title", { count: expired.length })}
					</Button>
					{showExpired && (
						<ul className="mt-2 divide-y divide-border/60 overflow-hidden rounded-lg border border-border/60">
							{expired.map((invite) => (
								<InviteRow key={invite.id} workspaceId={workspaceId} invite={invite} />
							))}
						</ul>
					)}
				</div>
			)}
		</section>
	);
}

function InviteRow({ workspaceId, invite }: { workspaceId: number; invite: Invite }) {
	const t = useTranslations("team");
	const format = useFormatter();
	const queryClient = useQueryClient();
	const [resending, setResending] = useState(false);
	const [notice, setNotice] = useState<string | null>(null);
	const [copied, setCopied] = useState(false);
	const showResending = useDelayedFlag(resending);
	const isPending = invite.status === "pending";
	const address = invite.email ?? "";
	const isLink = !invite.email;

	const date = (iso: string) => format.dateTime(new Date(iso), { dateStyle: "medium" });

	const refresh = () =>
		queryClient.invalidateQueries({ queryKey: cacheKeys.invites.all(workspaceId.toString()) });

	const resend = async () => {
		setResending(true);
		setNotice(null);
		try {
			await invitesApiService.resendInvite({ workspace_id: workspaceId, invite_id: invite.id });
			toast.success(t("resent", { email: address }));
			await refresh();
		} catch (error) {
			setNotice(apiErrorDetail(error, t("resend_failed")));
		} finally {
			setResending(false);
		}
	};

	const copy = async () => {
		const ok = await copyToClipboard(inviteLink(window.location.origin, invite.invite_code));
		setCopied(ok);
		if (ok) window.setTimeout(() => setCopied(false), 2000);
	};

	const revoke = async () => {
		try {
			await invitesApiService.deleteInvite({ workspace_id: workspaceId, invite_id: invite.id });
			toast.success(t("revoked"));
			await refresh();
		} catch (error) {
			setNotice(apiErrorDetail(error, t("revoke_failed")));
		}
	};

	return (
		<li
			className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center"
			data-testid="pending-invite"
		>
			<div className="flex min-w-0 flex-1 items-start gap-3">
				<span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-muted">
					{isLink ? (
						<Link2 className="size-4 text-muted-foreground" />
					) : (
						<Mail className="size-4 text-muted-foreground" />
					)}
				</span>
				<div className="min-w-0">
					<p className="truncate text-sm font-medium select-text">
						{isLink ? t("link_invite") : address}
					</p>
					<p className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
						{invite.role?.name && <span>{invite.role.name}</span>}
						{invite.name && <span>· {invite.name}</span>}
						<span>
							·{" "}
							{isLink
								? t("created_on", { date: date(invite.created_at) })
								: t("sent_on", { date: date(invite.last_sent_at ?? invite.created_at) })}
						</span>
						{isLink && (
							<span>
								·{" "}
								{invite.max_uses
									? t("uses_of", { used: invite.uses_count, max: invite.max_uses })
									: t("uses_count", { used: invite.uses_count })}
							</span>
						)}
						<span>
							·{" "}
							{invite.expires_at
								? isPending
									? t("expires_on", { date: date(invite.expires_at) })
									: t("status_expired")
								: t("no_expiry")}
						</span>
					</p>
					{notice && (
						<p role="alert" className="mt-1 text-xs text-destructive">
							{notice}
						</p>
					)}
				</div>
			</div>
			{isPending && (
				<div className="flex shrink-0 items-center gap-1 self-end sm:self-auto">
					{!isLink && (
						<Button
							variant="ghost"
							size="sm"
							className="gap-1.5"
							onClick={resend}
							disabled={resending}
						>
							{showResending ? <Spinner size="sm" /> : <RotateCw className="size-3.5" />}
							{t("resend")}
						</Button>
					)}
					<Button variant="ghost" size="sm" className="gap-1.5" onClick={copy}>
						{copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
						{copied ? t("link_copied") : t("copy_link")}
					</Button>
					<AlertDialog>
						<AlertDialogTrigger asChild>
							<Button
								variant="ghost"
								size="sm"
								className="gap-1.5 text-muted-foreground hover:text-destructive"
							>
								<Trash2 className="size-3.5" />
								{t("revoke")}
							</Button>
						</AlertDialogTrigger>
						<AlertDialogContent>
							<AlertDialogHeader>
								<AlertDialogTitle>{t("revoke_title")}</AlertDialogTitle>
								<AlertDialogDescription>
									{isLink ? t("revoke_body_link") : t("revoke_body", { email: address })}
								</AlertDialogDescription>
							</AlertDialogHeader>
							<AlertDialogFooter>
								<AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
								<AlertDialogAction
									onClick={revoke}
									className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
								>
									{t("revoke")}
								</AlertDialogAction>
							</AlertDialogFooter>
						</AlertDialogContent>
					</AlertDialog>
				</div>
			)}
		</li>
	);
}
