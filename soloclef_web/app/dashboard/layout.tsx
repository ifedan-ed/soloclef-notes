import type React from "react";
import { DashboardClientLayout } from "./client-layout";
import { DashboardShell } from "./dashboard-shell";

/**
 * Every signed-in page. The page is a static file, so nothing here knows who
 * is asking: DashboardShell asks the API for the session in the browser and
 * sends a stranger to /login, and the two remembered choices (the right
 * panel, the chat's retrieval scope) are read from their cookies in the
 * browser too, where they are written.
 */
export default function DashboardLayout({ children }: { children: React.ReactNode }) {
	return (
		<DashboardShell>
			<DashboardClientLayout>{children}</DashboardClientLayout>
		</DashboardShell>
	);
}
