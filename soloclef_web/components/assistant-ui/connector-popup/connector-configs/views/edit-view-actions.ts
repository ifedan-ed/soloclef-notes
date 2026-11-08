import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

/** What a connector's edit view offers, and how it words its destructive action. */
export interface EditViewActions {
	/**
	 * Another member's account-wide connection: shown read-only. The server
	 * lets only a connection's owner change or delete it (403 otherwise).
	 */
	readOnly: boolean;
	/** Whether there is anything the Save Changes button could save. */
	canSave: boolean;
	/** Whether a Disconnect button is offered. */
	canDisconnect: boolean;
	/** What Disconnect's confirm step says will happen. */
	disconnectWarning: string;
	/** The line under the connector's name. */
	subtitle: string;
	/** Email (IMAP) only: where its mailboxes are managed instead. */
	mailboxesNote: string | null;
}

export const EMAIL_MAILBOXES_NOTE =
	"Mailboxes are added and removed on the Email (IMAP) card, under Overview. Removing one there takes it out of every workspace.";

/**
 * Every non-MCP connection is account-wide (custom/ACCOUNT_CONNECTIONS.md):
 * on in every workspace its owner is in, and only its owner may change or
 * disconnect it. So Disconnect removes it from every workspace, and its
 * confirm step says so ("This will remove all indexed data" understated it,
 * and was wrong for Nextcloud and Google Drive, whose added files stay: both
 * disconnect through their own routes, use-connector-dialog).
 *
 * Email (IMAP) has nothing to save here and no Disconnect: its mailboxes are
 * added and removed one by one on its card, and the generic delete would
 * drop all of them at once. Its one control here is the "Enabled in this
 * workspace" switch.
 *
 * `hasSettingsPanel`: the view has a connector-specific settings component
 * for this type (Google Drive, Nextcloud, web pages, MCP).
 */
export function editViewActions(
	connector: Pick<SearchSourceConnector, "connector_type" | "is_mine" | "is_indexable" | "config">,
	{ hasSettingsPanel, hasVisionToggle }: { hasSettingsPanel: boolean; hasVisionToggle: boolean }
): EditViewActions {
	const isLive = Boolean(connector.config?.server_config);
	const isMCP = connector.connector_type === EnumConnectorName.MCP_CONNECTOR;
	const isEmail = connector.connector_type === EnumConnectorName.EMAIL_IMAP_CONNECTOR;
	const readOnly = !isMCP && connector.is_mine === false;

	const canSave =
		!isLive && !readOnly && (hasSettingsPanel || connector.is_indexable || hasVisionToggle);
	const canDisconnect = !readOnly && !isEmail;

	const disconnectWarning = isLive
		? "Your agent will lose access to this service"
		: connector.connector_type === EnumConnectorName.NEXTCLOUD_CONNECTOR
			? "This disconnects Nextcloud from every workspace. Files you already added stay."
			: connector.connector_type === EnumConnectorName.GOOGLE_DRIVE_CONNECTOR
				? "This disconnects Google Drive from every workspace. Files you already added stay."
				: "This disconnects it from every workspace and deletes the documents it brought into each.";

	const subtitle = isLive
		? "Manage your connected account"
		: readOnly
			? "Another member's connection. Only they can change or disconnect it."
			: isEmail
				? "Whether this workspace's chats and automations can read your mail"
				: "Manage your connector settings and sync configuration";

	return {
		readOnly,
		canSave,
		canDisconnect,
		disconnectWarning,
		subtitle,
		mailboxesNote: isEmail && !readOnly ? EMAIL_MAILBOXES_NOTE : null,
	};
}
