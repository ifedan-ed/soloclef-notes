import { redirect } from "next/navigation";
import { settingsPath } from "@/lib/dashboard-paths";

/** The automations list lives in settings; this address lands there. */
export default function AutomationsPage() {
	redirect(settingsPath("automations"));
}
