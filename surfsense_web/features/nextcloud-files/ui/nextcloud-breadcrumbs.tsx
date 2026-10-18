"use client";

import { ChevronRight, Cloud } from "lucide-react";
import { useTranslations } from "next-intl";
import { breadcrumbs } from "../lib/picker-selection";

/** Where the picker is, root first; every level but the last is a link back. */
export function NextcloudBreadcrumbs({
	path,
	onNavigate,
}: {
	path: string;
	onNavigate: (path: string) => void;
}) {
	const t = useTranslations("nextcloud_files");
	const crumbs = breadcrumbs(path);
	return (
		<nav aria-label={t("breadcrumbs_label")} className="min-w-0 overflow-x-auto">
			<ol className="flex items-center gap-1 whitespace-nowrap text-sm">
				{crumbs.map((crumb, index) => {
					const last = index === crumbs.length - 1;
					const label = crumb.path === "/" ? t("root") : crumb.name;
					return (
						<li key={crumb.path} className="flex items-center gap-1">
							{index > 0 ? (
								<ChevronRight aria-hidden="true" className="size-3.5 text-muted-foreground" />
							) : null}
							{last ? (
								<span aria-current="page" className="flex items-center gap-1 font-medium">
									{crumb.path === "/" ? <Cloud aria-hidden="true" className="size-3.5" /> : null}
									<span className="max-w-40 truncate">{label}</span>
								</span>
							) : (
								<button
									type="button"
									onClick={() => onNavigate(crumb.path)}
									className="flex items-center gap-1 rounded px-1 text-muted-foreground hover:text-foreground hover:underline"
								>
									{crumb.path === "/" ? <Cloud aria-hidden="true" className="size-3.5" /> : null}
									<span className="max-w-32 truncate">{label}</span>
								</button>
							)}
						</li>
					);
				})}
			</ol>
		</nav>
	);
}
