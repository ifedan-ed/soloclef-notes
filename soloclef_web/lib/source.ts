import { loader } from "fumadocs-core/source";
import {
	AlarmClock,
	Bell,
	BookOpen,
	Bot,
	CircleHelp,
	Compass,
	CreditCard,
	FolderOpen,
	Mail,
	MessageCircle,
	Search,
	Server,
	ShieldCheck,
	Smartphone,
	Sparkles,
	StickyNote,
	Users,
} from "lucide-react";
import { createElement } from "react";
import { docs } from "@/.source/server";

/** Explicit whitelist of Lucide icons used in docs frontmatter / meta.json.
 * Importing the full `icons` barrel would pull every Lucide icon (~1 400 SVGs)
 * into the docs bundle even though only a handful are referenced. Add new icons
 * here as docs pages are added.
 */
const DOCS_ICONS: Record<string, React.ComponentType> = {
	AlarmClock,
	Bell,
	BookOpen,
	Bot,
	CircleHelp,
	Compass,
	CreditCard,
	FolderOpen,
	Mail,
	MessageCircle,
	Search,
	Server,
	ShieldCheck,
	Smartphone,
	Sparkles,
	StickyNote,
	Users,
};

export const source = loader({
	baseUrl: "/docs",
	source: docs.toFumadocsSource(),
	icon(icon) {
		if (icon && icon in DOCS_ICONS) return createElement(DOCS_ICONS[icon]);
	},
});
