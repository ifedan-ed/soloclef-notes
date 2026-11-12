import type { Metadata, Viewport } from "next";
import "./globals.css";
import { RootProvider } from "fumadocs-ui/provider/next";
import { Roboto } from "next/font/google";
import { DesktopUpdateToast } from "@/components/desktop/desktop-update-toast";
import { AuthCutoverPurge } from "@/components/providers/AuthCutoverPurge";
import { ClientErrorReporter } from "@/components/providers/ClientErrorReporter";
import { GlobalLoadingProvider } from "@/components/providers/GlobalLoadingProvider";
import { I18nProvider } from "@/components/providers/I18nProvider";
import { PostHogProvider } from "@/components/providers/PostHogProvider";
import { ZeroProvider } from "@/components/providers/ZeroProvider";
import {
	OrganizationJsonLd,
	SoftwareApplicationJsonLd,
	WebSiteJsonLd,
} from "@/components/seo/json-ld";
import { ThemeProvider } from "@/components/theme/theme-provider";
import { Toaster } from "@/components/ui/sonner";
import { LocaleProvider } from "@/contexts/LocaleContext";
import { PlatformProvider } from "@/contexts/platform-context";
import { BRAND, metadataBase, siteUrl } from "@/lib/brand";
import { ReactQueryClientProvider } from "@/lib/query-client/query-client.provider";
import { cn } from "@/lib/utils";

const roboto = Roboto({
	subsets: ["latin"],
	weight: ["400", "500", "700"],
	// "optional": use Roboto only if it's ready almost immediately; otherwise
	// keep the fallback for this page view. "swap" (the default) always swaps
	// in Roboto once it arrives, which produced a visible font jump on load.
	display: "optional",
	// What "the fallback" is when Roboto misses that window. next/font's own
	// fallback face is Arial, by local("Arial") only, and the family it writes
	// ends there: on a device with no Arial (Linux, many Androids) that face
	// fails to load and the text drew in the browser's default font, a serif.
	// A generic family at the end makes the last resort the system's sans.
	fallback: ["system-ui", "sans-serif"],
	variable: "--font-roboto",
});

/**
 * Viewport configuration for mobile keyboard handling.
 * - interactiveWidget: 'resizes-content' tells mobile browsers (especially Chrome Android)
 *   to resize the CSS layout viewport when the virtual keyboard opens, so sticky elements
 *   (like the chat input bar) stay visible above the keyboard.
 * - viewportFit: 'cover' enables env(safe-area-inset-*) for notched/home-indicator devices.
 */
export const viewport: Viewport = {
	width: "device-width",
	initialScale: 1,
	viewportFit: "cover",
	interactiveWidget: "resizes-content",
};

export const metadata: Metadata = {
	metadataBase: metadataBase(),
	title: {
		default: BRAND.name,
		template: `%s`,
	},
	description: BRAND.description,
	applicationName: BRAND.name,
	appleWebApp: { title: BRAND.name, capable: true, statusBarStyle: "default" },
	openGraph: {
		title: BRAND.name,
		description: BRAND.description,
		url: siteUrl("/"),
		siteName: BRAND.name,
		type: "website",
		images: [{ url: BRAND.logo.og, width: 1200, height: 630, alt: BRAND.name }],
		locale: "en_US",
	},
	twitter: {
		card: "summary_large_image",
		title: BRAND.name,
		description: BRAND.description,
		images: [{ url: BRAND.logo.og, width: 1200, height: 630, alt: BRAND.name }],
	},
};

export default function RootLayout({
	children,
}: Readonly<{
	children: React.ReactNode;
}>) {
	// Using client-side i18n
	// Language can be switched dynamically through LanguageSwitcher component
	// Locale state is managed by LocaleContext and persisted in localStorage
	return (
		<html lang="en" suppressHydrationWarning>
			<head>
				<OrganizationJsonLd />
				<WebSiteJsonLd />
				<SoftwareApplicationJsonLd />
			</head>
			<body
				className={cn(
					roboto.className,
					roboto.variable,
					// No literal background here: `body` takes `bg-background` from the
					// base layer, so it follows the palette the theme class selects. A
					// utility class would sit in a later layer and win over it, pinning
					// the page dark whichever theme is active.
					"antialiased h-full w-full "
				)}
			>
				<PostHogProvider>
					<LocaleProvider>
						<I18nProvider>
							{/* Light, dark and system are all offered by the profile menu, so
							    the provider must resolve whichever one is stored rather than
							    pin a palette. Dark stays the default for anyone who has never
							    chosen. storageKey is left at "surfsense-theme": it is where the
							    menu already writes, and renaming it would discard the
							    preference of everyone who has set one. */}
							<ThemeProvider
								attribute="class"
								enableSystem
								disableTransitionOnChange
								defaultTheme="dark"
								storageKey="surfsense-theme"
							>
								<PlatformProvider>
									<RootProvider>
										<ReactQueryClientProvider>
											<AuthCutoverPurge />
											<ClientErrorReporter />
											<ZeroProvider>
												<GlobalLoadingProvider>{children}</GlobalLoadingProvider>
											</ZeroProvider>
										</ReactQueryClientProvider>
										<DesktopUpdateToast />
										<Toaster />
									</RootProvider>
								</PlatformProvider>
							</ThemeProvider>
						</I18nProvider>
					</LocaleProvider>
				</PostHogProvider>
			</body>
		</html>
	);
}
