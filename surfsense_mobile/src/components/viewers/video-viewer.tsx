import { useVideoPlayer, VideoView } from "expo-video";
import { useEffect, useState } from "react";
import { ScrollView, useWindowDimensions, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ArtifactManifest } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { primaryFile } from "../../lib/artifacts";
import { space } from "../../theme/colors";
import { MarkdownView } from "../markdown-view";
import { DelayedSpinner, Notice } from "../ui/primitives";

/** The rendered MP4, streamed with the session's bearer header, native controls and fullscreen. */
export function VideoViewer({ manifest }: { manifest: ArtifactManifest }) {
	const { api, t } = useApp();
	const primary = primaryFile(manifest);
	const [headers, setHeaders] = useState<Record<string, string> | null>(null);
	const [failed, setFailed] = useState(false);
	useEffect(() => {
		api
			.authHeaders()
			.then(setHeaders)
			.catch(() => setFailed(true));
	}, [api]);
	const insets = useSafeAreaInsets();
	if (failed)
		return (
			<View style={{ padding: space.lg }}>
				<Notice tone="error" message={t("mobile.output_failed")} />
			</View>
		);
	if (!headers || !primary) return <DelayedSpinner active style={{ marginTop: space.xxl }} />;
	return (
		<ScrollView contentContainerStyle={{ gap: space.lg, paddingBottom: insets.bottom + space.xxl }}>
			<Player uri={api.url(primary.content_url)} headers={headers} />
			{manifest.markdown_representation.trim() ? (
				<View style={{ paddingHorizontal: space.lg }}>
					<MarkdownView text={manifest.markdown_representation} />
				</View>
			) : null}
		</ScrollView>
	);
}

function Player({ uri, headers }: { uri: string; headers: Record<string, string> }) {
	const { width } = useWindowDimensions();
	const player = useVideoPlayer({ uri, headers }, (p) => {
		p.staysActiveInBackground = false;
	});
	return (
		<VideoView
			player={player}
			style={{ width, height: (width * 9) / 16, backgroundColor: "#000" }}
			nativeControls
			contentFit="contain"
			fullscreenOptions={{ enable: true }}
			allowsPictureInPicture
		/>
	);
}
