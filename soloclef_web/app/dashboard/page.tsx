import { redirect } from "next/navigation";
import { NOTEBOOKS_HOME } from "@/lib/dashboard-paths";

/** The account has one home, its notebooks: there is nothing to choose between. */
export default function DashboardPage() {
	redirect(NOTEBOOKS_HOME);
}
