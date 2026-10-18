/**
 * The web app's neutral shadcn palette (surfsense_web/app/globals.css), with
 * its oklch values converted to hex. Light and dark mirror :root and .dark.
 */
export interface Palette {
	background: string;
	foreground: string;
	card: string;
	popover: string;
	primary: string;
	primaryForeground: string;
	secondary: string;
	muted: string;
	mutedForeground: string;
	accent: string;
	border: string;
	input: string;
	ring: string;
	destructive: string;
	destructiveForeground: string;
	success: string;
	warning: string;
	overlay: string;
	citation: string;
}

export const light: Palette = {
	background: "#ffffff",
	foreground: "#0a0a0a",
	card: "#ffffff",
	popover: "#fcfcfc",
	primary: "#171717",
	primaryForeground: "#fafafa",
	secondary: "#f5f5f5",
	muted: "#f5f5f5",
	mutedForeground: "#737373",
	accent: "#f5f5f5",
	border: "#e5e5e5",
	input: "#e5e5e5",
	ring: "#a1a1a1",
	destructive: "#e7000b",
	destructiveForeground: "#fafafa",
	success: "#16a34a",
	warning: "#d97706",
	overlay: "rgba(0,0,0,0.4)",
	citation: "#e8eefc",
};

export const dark: Palette = {
	background: "#0a0a0a",
	foreground: "#fafafa",
	card: "#0a0a0a",
	popover: "#333333",
	primary: "#fafafa",
	primaryForeground: "#171717",
	secondary: "#262626",
	muted: "#262626",
	mutedForeground: "#a1a1a1",
	accent: "#262626",
	border: "#333333",
	input: "#333333",
	ring: "#575757",
	destructive: "#ff6467",
	destructiveForeground: "#fafafa",
	success: "#4ade80",
	warning: "#fbbf24",
	overlay: "rgba(0,0,0,0.6)",
	citation: "#1e2a44",
};

export const radius = { sm: 6, md: 10, lg: 14, xl: 20, pill: 999 } as const;
export const space = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 } as const;
