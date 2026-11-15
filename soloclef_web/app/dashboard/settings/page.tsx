import { ClientRedirect } from "@/components/routing/client-redirect";
import { settingsPath } from "@/lib/dashboard-paths";

/** Settings open on their first section. */
export default function SettingsPage() {
	return <ClientRedirect to={settingsPath("profile")} />;
}
