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
import { SegmentedControl } from "@/components/ui/segmented-control";
import { billingAdminApi } from "../../api/billing-admin-api";
import type { AdjustBody, AdminUser } from "../../api/billing-admin-schemas";
import { useAdminMutation } from "../../hooks/use-billing-admin";
import { adjustmentValid } from "../../lib/admin-forms";
import { parseNumber } from "../../lib/format";

/** Add or remove credits by hand, always with a reason for the ledger. */
export function AdjustCreditsDialog({
	user,
	onClose,
}: {
	user: AdminUser | null;
	onClose: () => void;
}) {
	return (
		<Dialog open={user !== null} onOpenChange={(open) => !open && onClose()}>
			<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
				{user ? <AdjustForm key={user.user_id} user={user} onClose={onClose} /> : null}
			</DialogContent>
		</Dialog>
	);
}

function AdjustForm({ user, onClose }: { user: AdminUser; onClose: () => void }) {
	const t = useTranslations("billing");
	const [amount, setAmount] = useState("");
	const [bucket, setBucket] = useState<"topup" | "plan">("topup");
	const [reason, setReason] = useState("");
	const save = useAdminMutation((body: AdjustBody) => billingAdminApi.adjust(user.user_id, body), {
		onSuccess: onClose,
	});
	const valid = adjustmentValid(amount, reason);
	return (
		<>
			<DialogHeader>
				<DialogTitle>{t("admin.adjust_credits")}</DialogTitle>
				<DialogDescription className="break-all">{user.email}</DialogDescription>
			</DialogHeader>
			<div className="space-y-1">
				<label htmlFor="adjust-amount" className="text-sm font-medium">
					{t("admin.adjust_amount")}
				</label>
				<Input
					id="adjust-amount"
					inputMode="decimal"
					value={amount}
					onChange={(e) => setAmount(e.target.value)}
					placeholder="100"
				/>
				<p className="text-xs text-muted-foreground">{t("admin.adjust_amount_hint")}</p>
			</div>
			<div className="space-y-1">
				<p className="text-sm font-medium">{t("admin.bucket")}</p>
				<SegmentedControl
					ariaLabel={t("admin.bucket")}
					value={bucket}
					onValueChange={(v) => setBucket(v as "topup" | "plan")}
					options={[
						{ value: "topup", label: t("admin.buckets.topup") },
						{ value: "plan", label: t("admin.buckets.plan") },
					]}
				/>
			</div>
			<div className="space-y-1">
				<label htmlFor="adjust-reason" className="text-sm font-medium">
					{t("admin.reason")}
				</label>
				<Input
					id="adjust-reason"
					value={reason}
					maxLength={300}
					onChange={(e) => setReason(e.target.value)}
					required
				/>
			</div>
			<DialogFooter>
				<Button variant="ghost" onClick={onClose}>
					{t("cancel")}
				</Button>
				<Button
					disabled={!valid || save.isPending}
					onClick={() =>
						save.mutate({ credits: parseNumber(amount), bucket, reason: reason.trim() })
					}
				>
					{t("admin.apply")}
				</Button>
			</DialogFooter>
		</>
	);
}
