"use client";

import { useQuery } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import {
	ChevronFirst,
	ChevronLast,
	ChevronLeft,
	ChevronRight,
	Clock,
	ShieldUser,
	User,
	Users,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useMemo, useState } from "react";
import { canPerform, membersAtom, myAccessAtom } from "@/atoms/members/members-query.atoms";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { InviteDialog } from "@/features/workspace-team/ui/invite-dialog";
import { MemberRow } from "@/features/workspace-team/ui/member-row";
import { PendingInvites } from "@/features/workspace-team/ui/pending-invites";
import { invitesApiService } from "@/lib/apis/invites-api.service";
import { rolesApiService } from "@/lib/apis/roles-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

const PAGE_SIZE = 10;
const SKELETON_KEYS = ["a", "b"];

interface TeamContentProps {
	workspaceId: number;
}

/** The Team page: members (role changes, removal) and the invitations still open. */
export function TeamContent({ workspaceId }: TeamContentProps) {
	const t = useTranslations("team");
	const { data: access = null, isLoading: accessLoading } = useAtomValue(myAccessAtom);
	const { data: members = [], isLoading: membersLoading } = useAtomValue(membersAtom);

	const canInvite = canPerform(access, "members:invite");
	const canManageRoles = canPerform(access, "members:manage_roles");
	const canRemove = canPerform(access, "members:remove");

	const { data: roles = [] } = useQuery({
		queryKey: cacheKeys.roles.all(workspaceId.toString()),
		queryFn: () => rolesApiService.getRoles({ workspace_id: workspaceId }),
		enabled: !!workspaceId,
	});

	const { data: invites = [], isLoading: invitesLoading } = useQuery({
		queryKey: cacheKeys.invites.all(workspaceId.toString()),
		queryFn: () => invitesApiService.getInvites({ workspace_id: workspaceId }),
		enabled: !!workspaceId && canInvite,
		staleTime: 60 * 1000,
	});

	const owners = useMemo(() => members.filter((m) => m.is_owner), [members]);
	const others = useMemo(() => members.filter((m) => !m.is_owner), [members]);

	const [pageIndex, setPageIndex] = useState(0);
	const total = others.length;
	const lastPage = Math.max(0, Math.ceil(total / PAGE_SIZE) - 1);
	useEffect(() => {
		if (pageIndex > lastPage) setPageIndex(lastPage);
	}, [pageIndex, lastPage]);
	const pageMembers = others.slice(pageIndex * PAGE_SIZE, (pageIndex + 1) * PAGE_SIZE);
	const displayStart = total > 0 ? pageIndex * PAGE_SIZE + 1 : 0;
	const displayEnd = Math.min((pageIndex + 1) * PAGE_SIZE, total);
	const canPrev = pageIndex > 0;
	const canNext = displayEnd < total;

	const loading = accessLoading || membersLoading;

	return (
		<div className="space-y-8">
			<div className="space-y-4 md:space-y-6">
				<div className="flex flex-wrap items-center justify-between gap-4">
					<div className="flex items-baseline gap-3">
						<h1 className="text-xl font-semibold text-foreground md:text-2xl">{t("title")}</h1>
						{!loading && (
							<p className="whitespace-nowrap text-sm text-muted-foreground">
								{t("member_count", { count: members.length })}
							</p>
						)}
					</div>
					{!loading && canInvite && <InviteDialog workspaceId={workspaceId} roles={roles} />}
				</div>

				<div className="overflow-hidden rounded-lg border border-border/60 bg-accent">
					<Table className="w-full table-fixed">
						<TableHeader>
							<TableRow className="border-b border-border/60 hover:bg-transparent">
								<TableHead className="w-[45%] border-r border-border/60 px-4 md:px-6">
									<span className="flex items-center gap-1.5 text-sm font-medium text-muted-foreground/70">
										<User size={14} className="opacity-60" />
										{t("col_name")}
									</span>
								</TableHead>
								<TableHead className="hidden w-[25%] border-r border-border/60 md:table-cell">
									<span className="flex items-center gap-1.5 text-sm font-medium text-muted-foreground/70">
										<Clock size={14} className="opacity-60" />
										{t("col_last_login")}
									</span>
								</TableHead>
								<TableHead className="w-[30%] px-4 md:px-6">
									<span className="flex items-center justify-end gap-1.5 text-sm font-medium text-muted-foreground/70">
										<ShieldUser size={14} className="opacity-60" />
										{t("col_role")}
									</span>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{loading ? (
								SKELETON_KEYS.map((key) => (
									<TableRow key={key} className="border-b border-border/60 hover:bg-transparent">
										<TableCell className="border-r border-border/60 px-4 py-2.5 md:px-6">
											<div className="flex items-center gap-3">
												<Skeleton className="size-10 shrink-0 rounded-full" />
												<Skeleton className="h-4 w-28 md:w-32" />
											</div>
										</TableCell>
										<TableCell className="hidden border-r border-border/60 py-2.5 md:table-cell">
											<Skeleton className="h-4 w-24" />
										</TableCell>
										<TableCell className="px-4 py-2.5 md:px-6">
											<div className="flex justify-end">
												<Skeleton className="h-4 w-12" />
											</div>
										</TableCell>
									</TableRow>
								))
							) : (
								<>
									{[...owners, ...pageMembers].map((member) => (
										<MemberRow
											key={member.id}
											workspaceId={workspaceId}
											member={member}
											roles={roles}
											canManageRoles={canManageRoles}
											canRemove={canRemove}
										/>
									))}
									{members.length === 0 && (
										<TableRow>
											<TableCell colSpan={3} className="py-12 text-center">
												<div className="flex flex-col items-center gap-2">
													<Users className="size-8 text-muted-foreground/50" />
													<p className="text-muted-foreground">{t("no_members")}</p>
												</div>
											</TableCell>
										</TableRow>
									)}
								</>
							)}
						</TableBody>
					</Table>
				</div>

				{total > PAGE_SIZE && (
					<div className="flex items-center justify-end gap-3 px-2">
						<span className="text-sm tabular-nums text-muted-foreground">
							{t("pagination", { start: displayStart, end: displayEnd, total })}
						</span>
						<div className="flex items-center gap-1">
							<Button
								variant="ghost"
								size="icon"
								className="size-8"
								onClick={() => setPageIndex(0)}
								disabled={!canPrev}
								aria-label={t("page_first")}
							>
								<ChevronFirst className="size-4" />
							</Button>
							<Button
								variant="ghost"
								size="icon"
								className="size-8"
								onClick={() => setPageIndex((i) => Math.max(0, i - 1))}
								disabled={!canPrev}
								aria-label={t("page_previous")}
							>
								<ChevronLeft className="size-4" />
							</Button>
							<Button
								variant="ghost"
								size="icon"
								className="size-8"
								onClick={() => setPageIndex((i) => (canNext ? i + 1 : i))}
								disabled={!canNext}
								aria-label={t("page_next")}
							>
								<ChevronRight className="size-4" />
							</Button>
							<Button
								variant="ghost"
								size="icon"
								className="size-8"
								onClick={() => setPageIndex(lastPage)}
								disabled={!canNext}
								aria-label={t("page_last")}
							>
								<ChevronLast className="size-4" />
							</Button>
						</div>
					</div>
				)}
			</div>

			{!loading &&
				canInvite &&
				(invitesLoading ? (
					<div className="space-y-3">
						<Skeleton className="h-5 w-40" />
						<Skeleton className="h-16 w-full rounded-lg" />
					</div>
				) : (
					<PendingInvites workspaceId={workspaceId} invites={invites} />
				))}
		</div>
	);
}
