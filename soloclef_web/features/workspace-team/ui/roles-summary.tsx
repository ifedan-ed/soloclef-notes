"use client";

import { Crown, Eye, PenLine } from "lucide-react";
import { useTranslations } from "next-intl";

const ROLES = [
	{ key: "owner", icon: Crown },
	{ key: "editor", icon: PenLine },
	{ key: "viewer", icon: Eye },
] as const;

/** What each of the three workspace roles may do, above the read-only role list. */
export function RolesSummary() {
	const t = useTranslations("team");
	return (
		<section className="space-y-3" aria-labelledby="roles-summary-title">
			<div>
				<h2 id="roles-summary-title" className="text-base font-semibold">
					{t("roles_title")}
				</h2>
				<p className="text-sm text-muted-foreground">{t("roles_intro")}</p>
			</div>
			<ul className="grid gap-2 sm:grid-cols-3">
				{ROLES.map(({ key, icon: Icon }) => (
					<li key={key} className="rounded-lg border border-border/60 p-3">
						<p className="flex items-center gap-2 text-sm font-medium">
							<Icon className="size-4 text-muted-foreground" />
							{t(`roles_${key}_name`)}
						</p>
						<p className="mt-1 text-xs text-muted-foreground">{t(`roles_${key}`)}</p>
					</li>
				))}
			</ul>
		</section>
	);
}
