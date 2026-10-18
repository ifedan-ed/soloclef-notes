import { setAudioModeAsync, useAudioPlayer, useAudioPlayerStatus } from "expo-audio";
import { useEffect, useState } from "react";
import { Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ArtifactManifest } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { primaryFile } from "../../lib/artifacts";
import { BRAND_NAME } from "../../lib/brand";
import { downloadToCache } from "../../lib/files";
import { formatDuration } from "../../lib/studio/catalog";
import { radius, space } from "../../theme/colors";
import { MarkdownView } from "../markdown-view";
import { AppText, Icon, IconButton, Notice, useDelayedFlag } from "../ui/primitives";

const SPEEDS = [0.75, 1, 1.25, 1.5, 2];

/**
 * Audio overview player. The file is fetched once with the session and played
 * from the device, so playback survives token refreshes and keeps going in
 * the background with lock-screen controls. The transcript (the artifact's
 * Markdown) reads underneath.
 */
export function AudioViewer({
	manifest,
	title,
	durationSeconds,
}: {
	manifest: ArtifactManifest;
	title: string;
	durationSeconds: number | null;
}) {
	const { api, t } = useApp();
	const primary = primaryFile(manifest);
	const [uri, setUri] = useState<string | null>(null);
	const [progress, setProgress] = useState(0);
	const [failed, setFailed] = useState(false);
	const [attempt, setAttempt] = useState(0);
	const showProgress = useDelayedFlag(!uri && !failed);

	useEffect(() => {
		if (!primary) return;
		let cancelled = false;
		downloadToCache(
			api,
			primary.content_url,
			primary.filename,
			`${manifest.artifact_id}-${manifest.generation}`,
			(f) => !cancelled && setProgress(f)
		)
			.then((file) => !cancelled && setUri(file.uri))
			.catch(() => !cancelled && setFailed(true));
		return () => {
			cancelled = true;
		};
	}, [api, primary, manifest.artifact_id, manifest.generation, attempt]);

	const insets = useSafeAreaInsets();
	return (
		<ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: insets.bottom + space.xxl }}>
			{failed ? (
				<Notice
					tone="error"
					message={t("mobile.download_failed")}
					actionLabel={t("notebooks.retry")}
					onAction={() => {
						setFailed(false);
						setAttempt((a) => a + 1);
					}}
				/>
			) : uri ? (
				<Player uri={uri} title={title} />
			) : showProgress ? (
				<AppText tone="muted">{`${t("mobile.loading")} ${Math.round(progress * 100)}%`}</AppText>
			) : null}
			{durationSeconds && !uri ? (
				<AppText variant="caption" tone="muted">
					{formatDuration(durationSeconds)}
				</AppText>
			) : null}
			{manifest.markdown_representation.trim() ? <MarkdownView text={manifest.markdown_representation} /> : null}
		</ScrollView>
	);
}

function Player({ uri, title }: { uri: string; title: string }) {
	const { t, colors } = useApp();
	const player = useAudioPlayer({ uri }, { updateInterval: 500 });
	const status = useAudioPlayerStatus(player);
	const [speed, setSpeed] = useState(1);
	const [barWidth, setBarWidth] = useState(1);

	useEffect(() => {
		// Background playback with lock-screen controls (Android needs the
		// lock-screen session to keep playing past a few minutes).
		setAudioModeAsync({ playsInSilentMode: true, shouldPlayInBackground: true, interruptionMode: "doNotMix" }).catch(
			() => {}
		);
		try {
			player.setActiveForLockScreen(true, { title, artist: BRAND_NAME });
		} catch {
			// not supported on this platform
		}
		return () => {
			try {
				player.clearLockScreenControls();
			} catch {
				// already released
			}
		};
	}, [player, title]);

	const duration = status.duration || 0;
	const position = Math.min(status.currentTime || 0, duration || Number.POSITIVE_INFINITY);
	const fraction = duration > 0 ? position / duration : 0;
	const seek = (seconds: number) => void player.seekTo(Math.max(0, Math.min(duration || seconds, seconds)));
	const toggle = () => {
		if (status.playing) {
			player.pause();
			return;
		}
		if (status.didJustFinish || (duration > 0 && position >= duration - 0.5)) void player.seekTo(0);
		player.play();
	};

	return (
		<View style={[styles.player, { borderColor: colors.border, backgroundColor: colors.muted }]}>
			<Pressable
				accessibilityRole="adjustable"
				accessibilityLabel={title}
				accessibilityValue={{ min: 0, max: Math.round(duration), now: Math.round(position) }}
				onLayout={(e) => setBarWidth(Math.max(1, e.nativeEvent.layout.width))}
				onPress={(e) => duration > 0 && seek((e.nativeEvent.locationX / barWidth) * duration)}
				style={styles.barHit}
			>
				<View style={[styles.bar, { backgroundColor: colors.border }]}>
					<View style={[styles.barFill, { width: `${fraction * 100}%`, backgroundColor: colors.foreground }]} />
				</View>
			</Pressable>
			<View style={styles.times}>
				<AppText variant="caption" tone="muted">
					{formatDuration(position)}
				</AppText>
				<AppText variant="caption" tone="muted">
					{duration > 0 ? formatDuration(duration) : "--:--"}
				</AppText>
			</View>
			<View style={styles.controls}>
				<IconButton name="play-back" label={t("mobile.skip_back")} onPress={() => seek(position - 15)} />
				<Pressable
					onPress={toggle}
					accessibilityRole="button"
					accessibilityLabel={status.playing ? t("mobile.pause") : t("notebooks.play")}
					style={[styles.play, { backgroundColor: colors.primary }]}
				>
					<Icon name={status.playing ? "pause" : "play"} size={28} color={colors.primaryForeground} />
				</Pressable>
				<IconButton name="play-forward" label={t("mobile.skip_forward")} onPress={() => seek(position + 30)} />
			</View>
			<Pressable
				onPress={() => {
					const next = SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length];
					setSpeed(next);
					player.setPlaybackRate(next);
				}}
				accessibilityRole="button"
				accessibilityLabel={`${t("mobile.playback_speed")} ${speed}x`}
				style={[styles.speed, { borderColor: colors.border }]}
			>
				<AppText variant="small">{`${speed}×`}</AppText>
			</Pressable>
		</View>
	);
}

const styles = StyleSheet.create({
	player: { borderWidth: 1, borderRadius: radius.lg, padding: space.lg, gap: space.sm },
	barHit: { paddingVertical: 10 },
	bar: { height: 4, borderRadius: 2, overflow: "hidden" },
	barFill: { height: 4 },
	times: { flexDirection: "row", justifyContent: "space-between" },
	controls: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xl },
	play: { width: 60, height: 60, borderRadius: 30, alignItems: "center", justifyContent: "center" },
	speed: {
		alignSelf: "center",
		borderWidth: 1,
		borderRadius: radius.pill,
		paddingHorizontal: space.md,
		paddingVertical: 4,
	},
});
