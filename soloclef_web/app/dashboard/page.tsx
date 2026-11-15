import { ClientRedirect } from "@/components/routing/client-redirect";
import { NOTEBOOKS_HOME } from "@/lib/dashboard-paths";

/** The account has one home, its notebooks: there is nothing to choose between. */
export default function DashboardPage() {
	return <ClientRedirect to={NOTEBOOKS_HOME} />;
}
