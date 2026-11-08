"use client";

import { useParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { RunsTable } from "@/features/api-runs/runs-table";
import { ApiKeyContent } from "../components/ApiKeyContent";

/** API Access: the personal token, and the scraper runs made with it or by the app. */
export default function Page() {
	const params = useParams();
	const t = useTranslations("userSettings");
	return (
		<div className="space-y-10">
			<ApiKeyContent />
			<section className="space-y-3">
				<div>
					<h2 className="text-base font-semibold">{t("api_runs_title")}</h2>
					<p className="text-sm text-muted-foreground">{t("api_runs_description")}</p>
				</div>
				<RunsTable workspaceId={Number(params.workspace_id)} />
			</section>
		</div>
	);
}
