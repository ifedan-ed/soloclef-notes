import { ClientRedirect } from "@/components/routing/client-redirect";
import { settingsPath } from "@/lib/dashboard-paths";

/** The automations list lives in settings; this address lands there. */
export default function AutomationsPage() {
	return <ClientRedirect to={settingsPath("automations")} />;
}
