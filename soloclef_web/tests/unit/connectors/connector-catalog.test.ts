import assert from "node:assert/strict";
import test from "node:test";
import { CONNECTOR_DISPLAY_DEFINITIONS } from "@/components/assistant-ui/connector-popup/constants/connector-constants";
import { searchSourceConnectorTypeEnum } from "@/contracts/types/connector.types";
import { documentTypeEnum } from "@/contracts/types/document.types";

// The backend enum was rebuilt with exactly these values; a row of any other
// type would fail the connector list's schema parse.
const KEPT_CONNECTOR_TYPES = [
	"EMAIL_IMAP_CONNECTOR",
	"GOOGLE_DRIVE_CONNECTOR",
	"MCP_CONNECTOR",
	"NEXTCLOUD_CONNECTOR",
	"WEBCRAWLER_CONNECTOR",
];

test("the connector catalog offers only the connectors that are kept", () => {
	const offered = [...new Set(CONNECTOR_DISPLAY_DEFINITIONS.map((c) => c.connectorType))].sort();
	assert.deepEqual(offered, KEPT_CONNECTOR_TYPES);
});

test("the connector type schema matches the backend's kept types", () => {
	assert.deepEqual([...searchSourceConnectorTypeEnum.options].sort(), KEPT_CONNECTOR_TYPES);
});

test("document types of removed connectors are gone", () => {
	for (const removed of ["NOTION_CONNECTOR", "GOOGLE_GMAIL_CONNECTOR", "DROPBOX_FILE"]) {
		assert.equal(documentTypeEnum.safeParse(removed).success, false, removed);
	}
	assert.equal(documentTypeEnum.safeParse("GOOGLE_DRIVE_FILE").success, true);
});
