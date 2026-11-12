import { getProviderIcon } from "@/lib/provider-icons";
import { modelFamily } from "./model-family";

/** The maker's mark beside a model name; the neutral chip when the name names no known family. */
export function ModelFamilyIcon({ name, className }: { name: string; className?: string }) {
	// An unknown key falls through to the provider icons' own neutral chip.
	return getProviderIcon(modelFamily(name) ?? "UNKNOWN", { className });
}
