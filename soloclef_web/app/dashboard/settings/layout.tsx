import type React from "react";
import { SettingsLayoutShell } from "./layout-shell";

export default function SettingsLayout({ children }: { children: React.ReactNode }) {
	return <SettingsLayoutShell>{children}</SettingsLayoutShell>;
}
