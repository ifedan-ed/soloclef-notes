import { useLocalSearchParams, useRouter } from "expo-router";
import { useState } from "react";
import { View } from "react-native";
import { Header, Screen, useToast } from "../../../../components/ui/layout";
import { DelayedSpinner, IconButton, Notice } from "../../../../components/ui/primitives";
import { AudioViewer } from "../../../../components/viewers/audio-viewer";
import { FileViewer } from "../../../../components/viewers/file-viewer";
import { FlashcardsViewer } from "../../../../components/viewers/flashcards-viewer";
import { MindMapViewer } from "../../../../components/viewers/mindmap-viewer";
import { QuizViewer } from "../../../../components/viewers/quiz-viewer";
import { VideoViewer } from "../../../../components/viewers/video-viewer";
import { useApp } from "../../../../lib/app-context";
import { primaryFile } from "../../../../lib/artifacts";
import { downloadToCache, shareFile } from "../../../../lib/files";
import { useManifest, useOutputs, useWorkspace } from "../../../../lib/queries";
import { space } from "../../../../theme/colors";

/**
 * A Studio output, opened natively where the phone can draw it (audio with
 * background playback, video, flashcards, quiz, mind map, images, reports as
 * text) and handed to the share sheet / another app otherwise.
 */
export default function OutputScreen() {
	const params = useLocalSearchParams<{ id: string; outputId: string }>();
	const notebookId = Number(params.id);
	const outputId = Number(params.outputId);
	const router = useRouter();
	const toast = useToast();
	const { t, api } = useApp();
	const { workspaceId } = useWorkspace();
	const outputs = useOutputs(workspaceId, notebookId);
	const output = outputs.data?.find((o) => o.id === outputId);
	const manifest = useManifest(workspaceId, output?.artifact_id);
	const [sharing, setSharing] = useState(false);
	const primary = primaryFile(manifest.data);

	const share = async () => {
		if (!primary || !manifest.data) return;
		setSharing(true);
		try {
			const file = await downloadToCache(
				api,
				primary.content_url,
				primary.filename,
				`${manifest.data.artifact_id}-${manifest.data.generation}`
			);
			await shareFile(file, primary.mime_type);
		} catch {
			toast(t("mobile.download_failed"), "error");
		} finally {
			setSharing(false);
		}
	};

	let body: React.ReactNode = null;
	if (!output) {
		body = outputs.isLoading ? (
			<DelayedSpinner active style={{ marginTop: space.xxl }} />
		) : (
			<Pad>
				<Notice tone="error" message={t("mobile.output_failed")} />
			</Pad>
		);
	} else if (output.status !== "ready" || !output.artifact_id) {
		body = (
			<Pad>
				<Notice message={t("mobile.output_not_ready")} />
			</Pad>
		);
	} else if (manifest.isError) {
		body = (
			<Pad>
				<Notice
					tone="error"
					message={t("mobile.output_failed")}
					actionLabel={t("notebooks.retry")}
					onAction={() => manifest.refetch()}
				/>
			</Pad>
		);
	} else if (!manifest.data) {
		body = <DelayedSpinner active style={{ marginTop: space.xxl }} />;
	} else {
		const m = manifest.data;
		switch (output.kind) {
			case "audio":
				body = <AudioViewer manifest={m} title={output.title} durationSeconds={output.duration_seconds ?? null} />;
				break;
			case "video":
				body = primary?.mime_type.startsWith("video/") ? <VideoViewer manifest={m} /> : <FileViewer manifest={m} />;
				break;
			case "flashcards":
				body = <FlashcardsViewer manifest={m} />;
				break;
			case "quiz":
				body = <QuizViewer manifest={m} />;
				break;
			case "mindmap":
				body = <MindMapViewer manifest={m} />;
				break;
			default:
				body = <FileViewer manifest={m} />;
		}
	}

	return (
		<Screen>
			<Header
				onBack={() => router.back()}
				backLabel={t("notebooks.back_to_studio")}
				title={output?.title ?? ""}
				subtitle={output ? t(`notebooks.kinds.${output.kind}.label`) : undefined}
				right={
					primary ? (
						<IconButton name="share-outline" label={t("mobile.share")} onPress={share} disabled={sharing} />
					) : null
				}
			/>
			<View style={{ flex: 1 }}>{body}</View>
		</Screen>
	);
}

function Pad({ children }: { children: React.ReactNode }) {
	return <View style={{ padding: space.lg }}>{children}</View>;
}
