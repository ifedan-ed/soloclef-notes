// Helper function to get connector type display name
export const getConnectorTypeDisplay = (type: string): string => {
	const typeMap: Record<string, string> = {
		GOOGLE_DRIVE_CONNECTOR: "Google Drive",
		WEBCRAWLER_CONNECTOR: "Web Pages",
		NEXTCLOUD_CONNECTOR: "Nextcloud",
		MCP_CONNECTOR: "MCP Server",
	};
	return typeMap[type] || type;
};
