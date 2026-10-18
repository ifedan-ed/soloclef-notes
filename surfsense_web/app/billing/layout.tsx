import { DashboardShell } from "@/app/dashboard/dashboard-shell";
import { RuntimeConfig } from "@/components/providers/runtime-config.server";

/** Signed-in only, like the dashboard: /billing is not a public route. */
export default function BillingLayout({ children }: { children: React.ReactNode }) {
	return (
		<RuntimeConfig>
			<DashboardShell>{children}</DashboardShell>
		</RuntimeConfig>
	);
}
