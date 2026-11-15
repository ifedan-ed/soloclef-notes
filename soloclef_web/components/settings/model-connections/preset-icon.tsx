import { getProviderIcon } from "@/lib/provider-icons";
import type { ConnectionPreset } from "./connection-presets";

/**
 * A preset's icon. Kept apart from connection-presets.ts so the presets stay
 * plain data that a unit test can load without the icon files (SVG modules
 * only the bundler can read). null, a connection of a provider the server no
 * longer supports, gets the generic icon.
 */
export function presetIcon(preset: ConnectionPreset | null, className = "size-4") {
	return getProviderIcon(preset?.iconKey ?? "custom", { className });
}
