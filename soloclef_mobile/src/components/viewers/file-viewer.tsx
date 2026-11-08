import { useQuery } from "@tanstack/react-query";
import { Image } from "expo-image";
import * as WebBrowser from "expo-web-browser";
import { useEffect, useState } from "react";
import { ScrollView, useWindowDimensions, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import type { ArtifactFile, ArtifactManifest } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { previewFile, primaryFile } from "../../lib/artifacts";
import { downloadToCache, openExternally } from "../../lib/files";
import { space } from "../../theme/colors";
import { MarkdownView } from "../markdown-view";
import { useToast } from "../ui/layout";
import { Button, DelayedSpinner, Notice } from "../ui/primitives";

/**
 * Reports, slides, infographics and tables. Images draw natively (with the
 * session header); interactive HTML reports run in a WebView fed the fetched
 * page; documents show their text here and open in the phone's own app
 * (PDF viewer, Office) on request.
 */
export function FileViewer({ manifest }: { manifest: ArtifactManifest }) {
	const primary = primaryFile(manifest);
	if (primary?.mime_type.startsWith("image/")) return <ImageArtifact manifest={manifest} file={primary} />;
	if (primary?.mime_type === "text/html") return <HtmlArtifact file={primary} />;
	return <DocumentArtifact manifest={manifest} />;
}

function ImageArtifact({ manifest, file }: { manifest: ArtifactManifest; file: ArtifactFile }) {
	const { api, t } = useApp();
	const { width } = useWindowDimensions();
	const [headers, setHeaders] = useState<Record<string, string> | null>(null);
	const [failed, setFailed] = useState(false);
	const [ratio, setRatio] = useState(1);
	useEffect(() => {
		api
			.authHeaders()
			.then(setHeaders)
			.catch(() => setFailed(true));
	}, [api]);
	if (failed)
		return (
			<View style={{ padding: space.lg }}>
				<Notice tone="error" message={t("mobile.output_failed")} />
			</View>
		);
	if (!headers) return <DelayedSpinner active style={{ marginTop: space.xxl }} />;
	return (
		<ScrollView
			maximumZoomScale={4}
			minimumZoomScale={1}
			centerContent
			contentContainerStyle={{ paddingBottom: space.xxl, gap: space.lg }}
		>
			<Image
				source={{ uri: api.url(file.content_url), headers, cacheKey: `${manifest.artifact_id}-${manifest.generation}` }}
				style={{ width, height: width * ratio }}
				contentFit="contain"
				onLoad={(e) => e.source.width > 0 && setRatio(e.source.height / e.source.width)}
				onError={() => setFailed(true)}
				accessibilityLabel={manifest.title}
			/>
			<View style={{ paddingHorizontal: space.lg }}>
				<OpenButton manifest={manifest} file={file} />
			</View>
		</ScrollView>
	);
}

function HtmlArtifact({ file }: { file: ArtifactFile }) {
	const { api, t } = useApp();
	const page = useQuery({
		queryKey: ["artifact-html", file.content_url],
		staleTime: Number.POSITIVE_INFINITY,
		queryFn: async () => {
			const response = await api.fetchAuthed(file.content_url);
			if (!response.ok) throw new Error(String(response.status));
			return response.text();
		},
	});
	if (page.isLoading) return <DelayedSpinner active style={{ marginTop: space.xxl }} />;
	if (!page.data)
		return (
			<View style={{ padding: space.lg }}>
				<Notice
					tone="error"
					message={t("mobile.output_failed")}
					actionLabel={t("notebooks.retry")}
					onAction={() => page.refetch()}
				/>
			</View>
		);
	return (
		<WebView
			source={{ html: page.data }}
			originWhitelist={["about:blank", "about:srcdoc", "data:*"]}
			javaScriptEnabled
			setSupportMultipleWindows={false}
			allowsBackForwardNavigationGestures={false}
			// Links in a report open in the browser, never inside the report view.
			onShouldStartLoadWithRequest={(request) => {
				if (/^https?:\/\//i.test(request.url)) {
					void WebBrowser.openBrowserAsync(request.url);
					return false;
				}
				return true;
			}}
			style={{ flex: 1, backgroundColor: "transparent" }}
		/>
	);
}

function DocumentArtifact({ manifest }: { manifest: ArtifactManifest }) {
	const insets = useSafeAreaInsets();
	const primary = primaryFile(manifest);
	const preview = previewFile(manifest);
	return (
		<ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: insets.bottom + space.xxl }}>
			<View style={{ flexDirection: "row", gap: space.sm, flexWrap: "wrap" }}>
				{primary ? <OpenButton manifest={manifest} file={primary} /> : null}
				{preview && preview.mime_type === "application/pdf" && primary?.mime_type !== "application/pdf" ? (
					<OpenButton manifest={manifest} file={preview} label="PDF" />
				) : null}
			</View>
			{manifest.markdown_representation.trim() ? <MarkdownView text={manifest.markdown_representation} /> : null}
		</ScrollView>
	);
}

function OpenButton({ manifest, file, label }: { manifest: ArtifactManifest; file: ArtifactFile; label?: string }) {
	const { api, t } = useApp();
	const toast = useToast();
	const [busy, setBusy] = useState(false);
	return (
		<Button
			kind="outline"
			icon="open-outline"
			busy={busy}
			title={label ? `${t("mobile.open_in_app")} (${label})` : t("mobile.open_in_app")}
			onPress={async () => {
				setBusy(true);
				try {
					const local = await downloadToCache(
						api,
						file.content_url,
						file.filename,
						`${manifest.artifact_id}-${manifest.generation}`
					);
					await openExternally(local, file.mime_type);
				} catch {
					toast(t("mobile.download_failed"), "error");
				} finally {
					setBusy(false);
				}
			}}
		/>
	);
}
