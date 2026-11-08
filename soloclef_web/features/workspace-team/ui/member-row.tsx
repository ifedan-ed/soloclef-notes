"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ChevronDown } from "lucide-react";
import { useTranslations } from "next-intl";
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
} from "@/components/ui/alert-dialog";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { TableCell, TableRow } from "@/components/ui/table";
import type { Membership } from "@/contracts/types/members.types";
import type { Role } from "@/contracts/types/roles.types";
import { membersApiService } from "@/lib/apis/members-api.service";
import { formatRelativeDate } from "@/lib/format-date";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { apiErrorDetail } from "../lib/api-error-detail";
import { assignableRoles } from "../lib/assignable-roles";

export function memberInitials(member: Membership): string {
	if (member.user_display_name) {
		const parts = member.user_display_name.trim().split(/\s+/);
		if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
		return member.user_display_name.slice(0, 2).toUpperCase();
	}
	if (member.user_email) return member.user_email.split("@")[0].slice(0, 2).toUpperCase();
	return "U";
}

/** One member: who, when they last signed in, and their role (changeable if allowed). */
export function MemberRow({
	workspaceId,
	member,
	roles,
	canManageRoles,
	canRemove,
}: {
	workspaceId: number;
	member: Membership;
	roles: Role[];
	canManageRoles: boolean;
	canRemove: boolean;
}) {
	const t = useTranslations("team");
	const queryClient = useQueryClient();
	const [confirmRemove, setConfirmRemove] = useState(false);
	const displayName = member.user_display_name || member.user_email || "—";
	const roleName = member.is_owner ? t("role_owner") : member.role?.name || t("role_none");
	const offered = assignableRoles(roles).filter((role) => role.id !== member.role_id);
	const showActions = !member.is_owner && (canManageRoles || canRemove);

	const refresh = () =>
		queryClient.invalidateQueries({ queryKey: cacheKeys.members.all(workspaceId.toString()) });

	const changeRole = async (role: Role) => {
		try {
			await membersApiService.updateMember({
				workspace_id: workspaceId,
				membership_id: member.id,
				data: { role_id: role.id },
			});
			toast.success(t("role_changed", { name: displayName, role: role.name }));
			await refresh();
		} catch (error) {
			toast.error(apiErrorDetail(error, t("change_failed")));
		}
	};

	const remove = async () => {
		try {
			await membersApiService.deleteMember({ workspace_id: workspaceId, membership_id: member.id });
			toast.success(t("member_removed"));
			await refresh();
		} catch (error) {
			toast.error(apiErrorDetail(error, t("remove_failed")));
		}
	};

	return (
		<TableRow className="border-b border-border/60 hover:bg-accent/50" data-testid="member-row">
			<TableCell className="w-[45%] max-w-0 border-r border-border/60 px-4 py-2.5 md:px-6">
				<div className="flex items-center gap-3">
					<Avatar className="size-10 shrink-0">
						{member.user_avatar_url && (
							<AvatarImage src={member.user_avatar_url} alt={displayName} />
						)}
						<AvatarFallback className="bg-popover text-sm text-popover-foreground">
							{memberInitials(member)}
						</AvatarFallback>
					</Avatar>
					<div className="min-w-0">
						<p className="truncate text-sm font-medium select-text">{displayName}</p>
						{member.user_display_name && member.user_email && (
							<p className="truncate text-xs text-muted-foreground select-text">
								{member.user_email}
							</p>
						)}
					</div>
				</div>
			</TableCell>
			<TableCell className="hidden w-[25%] border-r border-border/60 py-2.5 text-sm md:table-cell">
				{member.user_last_login ? formatRelativeDate(member.user_last_login) : t("never_logged_in")}
			</TableCell>
			<TableCell className="w-[30%] px-4 py-2.5 text-right md:px-6">
				{showActions ? (
					<>
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									className="h-auto gap-1.5 px-0 py-0 text-sm text-muted-foreground hover:bg-transparent hover:text-accent-foreground"
									aria-label={t("member_actions", { name: displayName })}
								>
									{roleName}
									<ChevronDown className="size-4" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent
								align="end"
								onCloseAutoFocus={(e) => e.preventDefault()}
								className="min-w-[140px]"
							>
								{canManageRoles &&
									offered.map((role) => (
										<DropdownMenuItem key={role.id} onSelect={() => void changeRole(role)}>
											{t("make_role", { role: role.name })}
										</DropdownMenuItem>
									))}
								{canManageRoles && canRemove && offered.length > 0 && <DropdownMenuSeparator />}
								{canRemove && (
									<DropdownMenuItem
										className="text-destructive focus:text-destructive"
										onSelect={() => setConfirmRemove(true)}
									>
										{t("remove")}
									</DropdownMenuItem>
								)}
							</DropdownMenuContent>
						</DropdownMenu>
						<AlertDialog open={confirmRemove} onOpenChange={setConfirmRemove}>
							<AlertDialogContent>
								<AlertDialogHeader>
									<AlertDialogTitle>{t("remove_title")}</AlertDialogTitle>
									<AlertDialogDescription>
										{t("remove_body", { email: member.user_email ?? displayName })}
									</AlertDialogDescription>
								</AlertDialogHeader>
								<AlertDialogFooter>
									<AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
									<AlertDialogAction
										onClick={() => void remove()}
										className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
									>
										{t("remove")}
									</AlertDialogAction>
								</AlertDialogFooter>
							</AlertDialogContent>
						</AlertDialog>
					</>
				) : (
					<span className="text-sm">{roleName}</span>
				)}
			</TableCell>
		</TableRow>
	);
}
