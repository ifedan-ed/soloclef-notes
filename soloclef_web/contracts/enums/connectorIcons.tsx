import {
	Brain,
	File,
	FileText,
	Globe,
	Mail,
	Microscope,
	Search,
	Sparkles,
	Telescope,
	Webhook,
} from "lucide-react";
import Image from "next/image";
import { EnumConnectorName } from "./connector";

export const getConnectorIcon = (connectorType: EnumConnectorName | string, className?: string) => {
	const iconProps = { className: className || "h-4 w-4" };
	const imgProps = {
		className: `${className || "h-5 w-5"} select-none pointer-events-none`,
		width: 20,
		height: 20,
		draggable: false as const,
	};

	switch (connectorType) {
		case EnumConnectorName.NEXTCLOUD_CONNECTOR:
			return <Image src="/connectors/nextcloud.svg" alt="Nextcloud" {...imgProps} />;
		case EnumConnectorName.GOOGLE_DRIVE_CONNECTOR:
			return <Image src="/connectors/google-drive.svg" alt="Google Drive" {...imgProps} />;
		case EnumConnectorName.WEBCRAWLER_CONNECTOR:
			return <Globe {...iconProps} />;
		case EnumConnectorName.EMAIL_IMAP_CONNECTOR:
			return <Mail {...iconProps} />;
		case EnumConnectorName.MCP_CONNECTOR:
			// Masked so the black glyph inherits currentColor (white in dark mode).
			return (
				<span
					aria-hidden="true"
					className={`${className || "h-5 w-5"} bg-current select-none pointer-events-none`}
					style={{
						mask: "url('/connectors/modelcontextprotocol.svg') center / contain no-repeat",
						WebkitMask: "url('/connectors/modelcontextprotocol.svg') center / contain no-repeat",
					}}
				/>
			);
		// Document types and search modes that share this icon set
		case "CRAWLED_URL":
			return <Globe {...iconProps} />;
		case "YOUTUBE_VIDEO":
			return <Image src="/connectors/youtube.svg" alt="YouTube" {...imgProps} />;
		case "FILE":
			return <File {...iconProps} />;
		case "GOOGLE_DRIVE_FILE":
			return <Image src="/connectors/google-drive.svg" alt="Google Drive" {...imgProps} />;
		case "NOTE":
			return <FileText {...iconProps} />;
		case "EXTENSION":
			return <Webhook {...iconProps} />;
		case "USER_MEMORY":
		case "TEAM_MEMORY":
			return <Brain {...iconProps} />;
		case "DEEP":
			return <Sparkles {...iconProps} />;
		case "DEEPER":
			return <Microscope {...iconProps} />;
		case "DEEPEST":
			return <Telescope {...iconProps} />;
		case "LOCAL_FOLDER_FILE":
			return <File {...iconProps} />;
		default:
			return <Search {...iconProps} />;
	}
};
