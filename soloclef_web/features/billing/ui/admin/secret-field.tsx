"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { SecretStatus } from "../../api/billing-admin-schemas";

/**
 * A write-only key: shows only whether it is set and where from. Typing a new
 * value replaces it on save; Clear removes the admin-set value (an env value
 * then shows through).
 */
export function SecretField({
	id,
	label,
	status,
	value,
	onChange,
	onClear,
	clearing,
}: {
	id: string;
	label: string;
	status: SecretStatus | undefined;
	value: string;
	onChange: (value: string) => void;
	onClear: () => void;
	clearing: boolean;
}) {
	const t = useTranslations("billing");
	const state = !status?.set
		? t("admin.secret_not_set")
		: status.source === "env"
			? t("admin.secret_set_env")
			: t("admin.secret_set_admin");
	return (
		<div className="space-y-1.5">
			<div className="flex flex-wrap items-baseline justify-between gap-2">
				<label htmlFor={id} className="text-sm font-medium">
					{label}
				</label>
				<span className="text-xs text-muted-foreground" data-testid={`${id}-status`}>
					{state}
				</span>
			</div>
			<div className="flex gap-2">
				<Input
					id={id}
					type="password"
					autoComplete="new-password"
					placeholder={status?.set ? t("admin.secret_replace_placeholder") : ""}
					value={value}
					onChange={(event) => onChange(event.target.value)}
				/>
				{status?.source === "admin" ? (
					<Button type="button" variant="outline" onClick={onClear} disabled={clearing}>
						{t("admin.clear")}
					</Button>
				) : null}
			</div>
		</div>
	);
}
