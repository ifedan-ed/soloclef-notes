import { useState } from "react";
import { Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ArtifactManifest } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { quizSchema, quizScore, useArtifactJson } from "../../lib/artifacts";
import { useWorkspace } from "../../lib/queries";
import { radius, space } from "../../theme/colors";
import { MarkdownView } from "../markdown-view";
import { AppText, Button, DelayedSpinner, Icon, Notice } from "../ui/primitives";

/** Native quiz: pick an answer, check it, read the explanation, see the score. */
export function QuizViewer({ manifest }: { manifest: ArtifactManifest }) {
	const { t, colors } = useApp();
	const insets = useSafeAreaInsets();
	const { workspaceId } = useWorkspace();
	const quiz = useArtifactJson(workspaceId, manifest, quizSchema);
	const [index, setIndex] = useState(0);
	const [answers, setAnswers] = useState<(number | null)[]>([]);
	const [checked, setChecked] = useState(false);
	const [finished, setFinished] = useState(false);

	if (quiz.isLoading) return <DelayedSpinner active style={{ marginTop: space.xxl }} />;
	if (quiz.isError || !quiz.data) {
		return (
			<View style={{ padding: space.lg }}>
				<Notice
					tone="error"
					message={t("mobile.output_failed")}
					actionLabel={t("notebooks.retry")}
					onAction={() => quiz.refetch()}
				/>
			</View>
		);
	}
	const questions = quiz.data.questions;
	const restart = () => {
		setIndex(0);
		setAnswers([]);
		setChecked(false);
		setFinished(false);
	};

	if (finished) {
		const score = quizScore(quiz.data, answers);
		return (
			<ScrollView
				contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: insets.bottom + space.xxl }}
			>
				<View style={[styles.score, { borderColor: colors.border }]}>
					<AppText variant="title">{`${Math.round((score / questions.length) * 100)}%`}</AppText>
					<AppText tone="muted">{t("mobile.score", { correct: score, total: questions.length })}</AppText>
				</View>
				{questions.map((q, i) => {
					const right = answers[i] === q.correct_option_index;
					return (
						<View key={i} style={[styles.review, { borderColor: colors.border }]}>
							<View style={{ flexDirection: "row", gap: space.sm, alignItems: "center" }}>
								<Icon
									name={right ? "checkmark-circle" : "close-circle"}
									color={right ? colors.success : colors.destructive}
								/>
								<AppText variant="label" style={{ flex: 1 }}>
									{t("mobile.question_position", { current: i + 1, total: questions.length })}
								</AppText>
							</View>
							<MarkdownView text={q.question_text} compact />
							<AppText variant="small" tone="muted">
								{q.options[q.correct_option_index]}
							</AppText>
						</View>
					);
				})}
				<Button icon="refresh" title={t("mobile.restart")} onPress={restart} />
			</ScrollView>
		);
	}

	const question = questions[index];
	const chosen = answers[index] ?? null;
	const correct = chosen === question.correct_option_index;
	const last = index === questions.length - 1;

	return (
		<ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: insets.bottom + space.xxl }}>
			<AppText variant="small" tone="muted">
				{t("mobile.question_position", { current: index + 1, total: questions.length })}
			</AppText>
			<MarkdownView text={question.question_text} />
			<View style={{ gap: space.sm }}>
				{question.options.map((option, i) => {
					const isChosen = chosen === i;
					const isAnswer = checked && i === question.correct_option_index;
					const isWrong = checked && isChosen && !correct;
					return (
						<Pressable
							key={i}
							disabled={checked}
							onPress={() =>
								setAnswers((current) => {
									const next = [...current];
									next[index] = i;
									return next;
								})
							}
							accessibilityRole="radio"
							accessibilityState={{ selected: isChosen, disabled: checked }}
							style={[
								styles.option,
								{
									borderColor: isAnswer
										? colors.success
										: isWrong
											? colors.destructive
											: isChosen
												? colors.foreground
												: colors.border,
									backgroundColor: isChosen ? colors.accent : colors.background,
								},
							]}
						>
							<AppText variant="label" tone="muted">
								{String.fromCharCode(65 + i)}
							</AppText>
							<View style={{ flex: 1 }}>
								<MarkdownView text={option} compact />
							</View>
							{isAnswer ? (
								<Icon name="checkmark-circle" color={colors.success} />
							) : isWrong ? (
								<Icon name="close-circle" color={colors.destructive} />
							) : null}
						</Pressable>
					);
				})}
			</View>
			{checked ? (
				<View style={[styles.explanation, { borderColor: correct ? colors.success : colors.destructive }]}>
					<AppText variant="label" tone={correct ? "success" : "destructive"}>
						{correct ? t("mobile.correct") : t("mobile.incorrect")}
					</AppText>
					<MarkdownView text={question.explanation_text} compact />
				</View>
			) : null}
			<View style={{ flexDirection: "row", gap: space.sm }}>
				<Button
					kind="outline"
					title={t("mobile.previous")}
					disabled={index === 0}
					onPress={() => {
						setIndex(index - 1);
						setChecked(answers[index - 1] !== undefined && answers[index - 1] !== null);
					}}
					style={{ flex: 1 }}
				/>
				{!checked ? (
					<Button
						title={t("mobile.check_answer")}
						disabled={chosen === null}
						onPress={() => setChecked(true)}
						style={{ flex: 1 }}
					/>
				) : last ? (
					<Button title={t("mobile.see_score")} onPress={() => setFinished(true)} style={{ flex: 1 }} />
				) : (
					<Button
						title={t("mobile.next")}
						onPress={() => {
							setIndex(index + 1);
							setChecked(answers[index + 1] !== undefined && answers[index + 1] !== null);
						}}
						style={{ flex: 1 }}
					/>
				)}
			</View>
		</ScrollView>
	);
}

const styles = StyleSheet.create({
	option: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.md,
		borderWidth: 1,
		borderRadius: radius.md,
		padding: space.md,
	},
	explanation: { borderWidth: 1, borderRadius: radius.md, padding: space.md, gap: space.sm },
	score: { alignItems: "center", gap: space.xs, borderWidth: 1, borderRadius: radius.lg, padding: space.xl },
	review: { borderWidth: 1, borderRadius: radius.md, padding: space.md, gap: space.sm },
});
