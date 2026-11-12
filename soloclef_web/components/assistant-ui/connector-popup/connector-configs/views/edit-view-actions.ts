import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

/** What a connector's edit view offers, and how it words its destructive action. */
export interface EditViewActions {
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
	"Mailboxes are added and removed on the Email (IMAP) card, under Overview.";

/**
 * Every connection listed is the signed-in account's own (the server lists
 * no one else's), so there is no read-only case: what the view offers
 * depends only on the connection's type.
 *
 * Disconnect's confirm step says what is lost. Nextcloud and Google Drive
 * disconnect through their own routes and the files already added stay
 * (use-connector-dialog); any other connection takes its documents with it.
 *
 * Email (IMAP) has nothing to save here and no Disconnect: its mailboxes are
 * added and removed one by one on its card, and the generic delete would
 * drop all of them at once. Its one control here is the "Enabled" switch.
 *
 * `hasSettingsPanel`: the view has a connector-specific settings component
 * for this type (Google Drive, Nextcloud, web pages, MCP).
 */
export function editViewActions(
	connector: Pick<SearchSourceConnector, "connector_type" | "is_indexable" | "config">,
	{ hasSettingsPanel, hasVisionToggle }: { hasSettingsPanel: boolean; hasVisionToggle: boolean }
): EditViewActions {
	const isLive = Boolean(connector.config?.server_config);
	const isEmail = connector.connector_type === EnumConnectorName.EMAIL_IMAP_CONNECTOR;

	const canSave = !isLive && (hasSettingsPanel || connector.is_indexable || hasVisionToggle);
	const canDisconnect = !isEmail;

	const disconnectWarning = isLive
		? "Your agent will lose access to this service"
		: connector.connector_type === EnumConnectorName.NEXTCLOUD_CONNECTOR
			? "This disconnects Nextcloud. Files you already added stay."
			: connector.connector_type === EnumConnectorName.GOOGLE_DRIVE_CONNECTOR
				? "This disconnects Google Drive. Files you already added stay."
				: "This disconnects it and deletes the documents it brought in.";

	const subtitle = isLive
		? "Manage your connected account"
		: isEmail
			? "Whether your chats and automations can read your mail"
			: "Manage your connector settings and sync configuration";

	return {
		canSave,
		canDisconnect,
		disconnectWarning,
		subtitle,
		mailboxesNote: isEmail ? EMAIL_MAILBOXES_NOTE : null,
	};
}
