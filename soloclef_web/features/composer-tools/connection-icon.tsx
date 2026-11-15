import { Wrench } from "lucide-react";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { getConnectorIcon } from "@/contracts/enums/connectorIcons";
import { cn } from "@/lib/utils";

/**
 * The connection kinds the tool menu groups by (the `connector` of
 * GET /api/v1/agent/tools), drawn with the same icons the connectors list
 * uses, so a connection looks the same in the menu as in settings (the MCP
 * mark is masked there to follow the text colour, which keeps it visible in
 * the dark theme). An unknown kind gets the generic tool mark.
 */
const CONNECTOR_TYPES: Record<string, string> = {
	mail: EnumConnectorName.EMAIL_IMAP_CONNECTOR,
	google_drive: EnumConnectorName.GOOGLE_DRIVE_CONNECTOR,
	nextcloud: EnumConnectorName.NEXTCLOUD_CONNECTOR,
	mcp: EnumConnectorName.MCP_CONNECTOR,
};

export function ConnectionIcon({
	connector,
	className,
}: {
	connector?: string;
	className?: string;
}) {
	const type = connector ? CONNECTOR_TYPES[connector] : undefined;
	const sized = cn("size-4 shrink-0 text-muted-foreground", className);
	if (!type) return <Wrench className={sized} aria-hidden />;
	return getConnectorIcon(type, sized);
}
