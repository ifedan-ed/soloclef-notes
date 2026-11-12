import { cookies } from "next/headers";
import type React from "react";
import { RuntimeConfig } from "@/components/providers/runtime-config.server";
import {
	parseRetrievalScopeCookie,
	RETRIEVAL_SCOPE_COOKIE,
} from "@/lib/chat/retrieval-scope-preferences";
import { DashboardClientLayout } from "./client-layout";
import { DashboardShell } from "./dashboard-shell";

const RIGHT_PANEL_COLLAPSED_COOKIE = "surfsense_right_panel_collapsed";

/** The signed-in account's id, read from the session token without verifying it. */
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

/**
 * Every signed-in page. The two cookies are read on the server so the first
 * paint already has the right panel and the chat's retrieval scope as the
 * person left them; the id only decides whose saved scope the cookie is,
 * and the backend still checks every request itself.
 */
export default async function DashboardLayout({ children }: { children: React.ReactNode }) {
	const cookieStore = await cookies();
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
		<RuntimeConfig>
			<DashboardShell>
				<DashboardClientLayout
					initialRetrievalScope={initialRetrievalScope}
					initialRightPanelCollapsed={initialRightPanelCollapsed}
				>
					{children}
				</DashboardClientLayout>
			</DashboardShell>
		</RuntimeConfig>
	);
}
