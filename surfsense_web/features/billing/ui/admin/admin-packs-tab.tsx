"use client";

import { Plus } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
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
import { Switch } from "@/components/ui/switch";
import { billingAdminApi } from "../../api/billing-admin-api";
import type { AdminOverview, AdminPack, PackBody } from "../../api/billing-admin-schemas";
import { useAdminMutation } from "../../hooks/use-billing-admin";
import {
	type ContentLocale,
	draftToPackBody,
	emptyPackDraft,
	type PackDraft,
	packDraftValid,
	packToDraft,
} from "../../lib/admin-forms";
import { formatCredits, formatMoney } from "../../lib/format";
import { LocalePicker } from "./locale-picker";

/** Credit packs: create, edit, and deactivate (kept for payment history). */
export function AdminPacksTab({ overview }: { overview: AdminOverview }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const [editing, setEditing] = useState<AdminPack | "new" | null>(null);
	const [deactivating, setDeactivating] = useState<AdminPack | null>(null);
	const deactivate = useAdminMutation((id: number) => billingAdminApi.deactivatePack(id), {
		onSuccess: () => setDeactivating(null),
	});
	return (
		<div className="space-y-3">
			<div className="flex justify-end">
				<Button onClick={() => setEditing("new")}>
					<Plus className="size-4" aria-hidden />
					{t("admin.new_pack")}
				</Button>
			</div>
			<ul className="divide-y rounded-lg border">
				{overview.packs.map((pack) => (
					<li key={pack.id} className="flex flex-wrap items-center justify-between gap-2 p-3">
						<div className="min-w-0">
							<p className="flex flex-wrap items-center gap-2 font-medium">
								{pack.name}
								<span className="font-mono text-xs text-muted-foreground">{pack.slug}</span>
								{!pack.active ? <Badge variant="outline">{t("admin.inactive")}</Badge> : null}
							</p>
							<p className="text-sm text-muted-foreground">
								{t("pack_line", {
									credits: formatCredits(pack.credits, locale),
									price: formatMoney(pack.price_cents, pack.currency, locale),
								})}
							</p>
						</div>
						<div className="flex gap-2">
							<Button variant="outline" size="sm" onClick={() => setEditing(pack)}>
								{t("admin.edit")}
							</Button>
							{pack.active ? (
								<Button variant="ghost" size="sm" onClick={() => setDeactivating(pack)}>
									{t("admin.deactivate")}
								</Button>
							) : null}
						</div>
					</li>
				))}
			</ul>

			<Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
				<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
					{editing ? (
						<PackEditor
							key={editing === "new" ? "new" : editing.id}
							pack={editing}
							nextSortOrder={overview.packs.length}
							onClose={() => setEditing(null)}
						/>
					) : null}
				</DialogContent>
			</Dialog>

			<Dialog open={deactivating !== null} onOpenChange={(open) => !open && setDeactivating(null)}>
				<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
					<DialogHeader>
						<DialogTitle>{t("admin.deactivate_pack_title")}</DialogTitle>
						<DialogDescription>{t("admin.deactivate_pack_body")}</DialogDescription>
					</DialogHeader>
					<DialogFooter>
						<Button variant="ghost" onClick={() => setDeactivating(null)}>
							{t("cancel")}
						</Button>
						<Button
							variant="destructive"
							disabled={deactivate.isPending}
							onClick={() => deactivating && deactivate.mutate(deactivating.id)}
						>
							{t("admin.deactivate")}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}

function PackEditor({
	pack,
	nextSortOrder,
	onClose,
}: {
	pack: AdminPack | "new";
	nextSortOrder: number;
	onClose: () => void;
}) {
	const t = useTranslations("billing");
	const [draft, setDraft] = useState<PackDraft>(() =>
		pack === "new" ? emptyPackDraft(nextSortOrder) : packToDraft(pack)
	);
	const [contentLocale, setContentLocale] = useState<ContentLocale>("en");
	const save = useAdminMutation(
		(body: PackBody) =>
			pack === "new" ? billingAdminApi.createPack(body) : billingAdminApi.updatePack(pack.id, body),
		{ onSuccess: onClose }
	);
	const set = <K extends keyof PackDraft>(key: K, value: PackDraft[K]) =>
		setDraft((d) => ({ ...d, [key]: value }));
	const field = (id: string, label: string, key: "slug" | "credits" | "price" | "sort_order") => (
		<div className="space-y-1">
			<label htmlFor={id} className="text-sm font-medium">
				{label}
			</label>
			<Input
				id={id}
				value={draft[key]}
				inputMode={key === "slug" ? undefined : "decimal"}
				onChange={(e) => set(key, e.target.value)}
				autoComplete="off"
			/>
		</div>
	);
	return (
		<>
			<DialogHeader>
				<DialogTitle>{pack === "new" ? t("admin.new_pack") : t("admin.edit_pack")}</DialogTitle>
				<DialogDescription>{t("admin.pack_editor_description")}</DialogDescription>
			</DialogHeader>
			<div className="grid grid-cols-2 gap-3">
				{field("pack-slug", t("admin.slug"), "slug")}
				{field("pack-credits", t("admin.credits"), "credits")}
				{field("pack-price", t("admin.price"), "price")}
				{field("pack-sort", t("admin.sort_order"), "sort_order")}
			</div>
			<div className="flex items-center justify-between gap-2 rounded-md border p-2">
				<span className="text-sm">{t("admin.flags.active")}</span>
				<Switch
					checked={draft.active}
					onCheckedChange={(v) => set("active", v)}
					aria-label={t("admin.flags.active")}
				/>
			</div>
			<LocalePicker value={contentLocale} onChange={setContentLocale} />
			<div className="space-y-1">
				<label htmlFor="pack-name" className="text-sm font-medium">
					{t("admin.pack_name")}
				</label>
				<Input
					id="pack-name"
					value={draft.names[contentLocale] ?? ""}
					onChange={(e) => set("names", { ...draft.names, [contentLocale]: e.target.value })}
				/>
			</div>
			<DialogFooter>
				<Button variant="ghost" onClick={onClose}>
					{t("cancel")}
				</Button>
				<Button
					disabled={!packDraftValid(draft) || save.isPending}
					onClick={() => save.mutate(draftToPackBody(draft))}
				>
					{t("admin.save")}
				</Button>
			</DialogFooter>
		</>
	);
}
