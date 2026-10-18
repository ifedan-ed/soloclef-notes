"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { billingAdminApi } from "../../api/billing-admin-api";
import type { AccountUpdate, AdminPlan, AdminUser } from "../../api/billing-admin-schemas";
import { useAdminMutation } from "../../hooks/use-billing-admin";

/** One person's plan and exemption. */
export function UserAccountDialog({
	user,
	plans,
	onClose,
}: {
	user: AdminUser | null;
	plans: AdminPlan[];
	onClose: () => void;
}) {
	return (
		<Dialog open={user !== null} onOpenChange={(open) => !open && onClose()}>
			<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
				{user ? (
					<UserAccountForm key={user.user_id} user={user} plans={plans} onClose={onClose} />
				) : null}
			</DialogContent>
		</Dialog>
	);
}

function UserAccountForm({
	user,
	plans,
	onClose,
}: {
	user: AdminUser;
	plans: AdminPlan[];
	onClose: () => void;
}) {
	const t = useTranslations("billing");
	const [planId, setPlanId] = useState(String(user.account?.plan_id ?? ""));
	const [exempt, setExempt] = useState(user.account?.exempt ?? false);
	const [reason, setReason] = useState("");
	const save = useAdminMutation(
		(body: AccountUpdate) => billingAdminApi.updateUser(user.user_id, body),
		{ onSuccess: onClose }
	);
	const hasSubscription = user.account?.has_subscription ?? false;
	const submit = () => {
		const body: AccountUpdate = {};
		if (planId && Number(planId) !== user.account?.plan_id) body.plan_id = Number(planId);
		if (exempt !== (user.account?.exempt ?? false)) {
			body.exempt = exempt;
			if (exempt) body.exempt_reason = reason.trim();
		}
		if (Object.keys(body).length === 0) {
			onClose();
			return;
		}
		save.mutate(body);
	};
	return (
		<>
			<DialogHeader>
				<DialogTitle>{t("admin.manage_user")}</DialogTitle>
				<DialogDescription className="break-all">{user.email}</DialogDescription>
			</DialogHeader>
			<div className="space-y-1.5">
				<p className="text-sm font-medium" id="admin-user-plan">
					{t("admin.plan")}
				</p>
				<Select value={planId} onValueChange={setPlanId} disabled={hasSubscription}>
					<SelectTrigger aria-labelledby="admin-user-plan" className="w-full">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{plans.map((plan) => (
							<SelectItem key={plan.id} value={String(plan.id)}>
								{plan.name}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				{hasSubscription ? (
					<p className="text-xs text-muted-foreground">{t("admin.plan_locked_subscription")}</p>
				) : null}
			</div>
			<div className="flex items-center justify-between gap-2">
				<span className="text-sm font-medium">{t("admin.exempt")}</span>
				<Switch checked={exempt} onCheckedChange={setExempt} aria-label={t("admin.exempt")} />
			</div>
			{exempt && !(user.account?.exempt ?? false) ? (
				<div className="space-y-1">
					<label htmlFor="admin-exempt-reason" className="text-sm font-medium">
						{t("admin.exempt_reason")}
					</label>
					<Input
						id="admin-exempt-reason"
						value={reason}
						maxLength={300}
						onChange={(e) => setReason(e.target.value)}
					/>
				</div>
			) : null}
			<DialogFooter>
				<Button variant="ghost" onClick={onClose}>
					{t("cancel")}
				</Button>
				<Button onClick={submit} disabled={save.isPending}>
					{t("admin.save")}
				</Button>
			</DialogFooter>
		</>
	);
}
