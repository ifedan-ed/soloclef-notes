// Server component

import { cookies } from "next/headers";
import type React from "react";
import {
	parseRetrievalScopeCookie,
	RETRIEVAL_SCOPE_COOKIE,
} from "@/lib/chat/retrieval-scope-preferences";
import { DashboardClientLayout } from "./client-layout";

const RIGHT_PANEL_COLLAPSED_COOKIE = "surfsense_right_panel_collapsed";

function sessionUserId(token: string | undefined): string | undefined {
	if (!token) return undefined;
	try {
		const encodedPayload = decodeURIComponent(token).split(".")[1];
		if (!encodedPayload) return undefined;
		const payload = JSON.parse(Buffer.from(encodedPayload, "base64url").toString("utf8")) as {
			sub?: unknown;
		};
		return typeof payload.sub === "string" ? payload.sub : undefined;
	} catch {
		return undefined;
	}
}

export default async function DashboardLayout({
	params,
	children,
}: {
	params: Promise<{ workspace_id: string }>;
	children: React.ReactNode;
}) {
	const [{ workspace_id }, cookieStore] = await Promise.all([params, cookies()]);
	const initialRightPanelCollapsed =
		cookieStore.get(RIGHT_PANEL_COLLAPSED_COOKIE)?.value === "true";
	const currentUserId = sessionUserId(
		cookieStore.get(process.env.SESSION_COOKIE_NAME ?? "surfsense_session")?.value
	);
	const initialRetrievalScope = parseRetrievalScopeCookie(
		cookieStore.get(RETRIEVAL_SCOPE_COOKIE)?.value,
		currentUserId
	);

	return (
		<DashboardClientLayout
			workspaceId={workspace_id}
			initialRetrievalScope={initialRetrievalScope}
			initialRightPanelCollapsed={initialRightPanelCollapsed}
		>
			{children}
		</DashboardClientLayout>
	);
}
