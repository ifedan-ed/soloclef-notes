import { redirect } from "next/navigation";
import { settingsPath } from "@/lib/dashboard-paths";

export default function SettingsPage() {
	redirect(settingsPath("profile"));
}
