/**
 * Maps SearchSourceConnectorType to DocumentType for fetching document counts.
 * Connectors without a document type of their own (MCP servers, Nextcloud)
 * have no entry.
 */
export const CONNECTOR_TO_DOCUMENT_TYPE: Record<string, string> = {
	GOOGLE_DRIVE_CONNECTOR: "GOOGLE_DRIVE_FILE",
	WEBCRAWLER_CONNECTOR: "CRAWLED_URL",
};

/**
 * Get the document type for a given connector type
 * Returns undefined if the connector doesn't index documents (e.g., MCP servers)
 */
export function getDocumentTypeForConnector(connectorType: string): string | undefined {
	return CONNECTOR_TO_DOCUMENT_TYPE[connectorType];
}

/**
 * Get document count for a specific connector type from document type counts
 */
export function getDocumentCountForConnector(
	connectorType: string,
	documentTypeCounts: Record<string, number> | undefined
): number | undefined {
	if (!documentTypeCounts) return undefined;

	const documentType = getDocumentTypeForConnector(connectorType);
	if (!documentType) return undefined;

	return documentTypeCounts[documentType];
}

/**
 * Check if a connector type is indexable (produces documents)
 */
export function isIndexableConnectorType(connectorType: string): boolean {
	return connectorType in CONNECTOR_TO_DOCUMENT_TYPE;
}
