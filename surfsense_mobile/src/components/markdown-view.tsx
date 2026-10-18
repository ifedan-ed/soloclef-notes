import * as WebBrowser from "expo-web-browser";
import { Fragment, memo, type ReactNode, useMemo } from "react";
import { Platform, ScrollView, StyleSheet, Text, type TextStyle, View } from "react-native";
import { type CitationToken, CitationNumbering, parseCitations } from "../lib/chat/citations";
import { useApp } from "../lib/app-context";
import { type Block, type Inline, parseMarkdown } from "../lib/markdown/parse";
import type { Palette } from "../theme/colors";
import { radius, space } from "../theme/colors";

export type CitationHandler = (token: CitationToken, number: number) => void;

const MONO = Platform.select({ ios: "Menlo", default: "monospace" });

/**
 * Native Markdown for answers, guides and reports. Citation tokens become
 * numbered badges (numbered by first appearance) that open the cited chunk.
 */
export const MarkdownView = memo(function MarkdownView({
	text,
	onCitation,
	compact,
}: {
	text: string;
	onCitation?: CitationHandler;
	compact?: boolean;
}) {
	const { colors } = useApp();
	const blocks = useMemo(() => parseMarkdown(text), [text]);
	const numbering = new CitationNumbering();
	const ctx: RenderContext = { colors, numbering, onCitation };
	return (
		<View style={{ gap: compact ? space.sm : space.md }}>
			{blocks.map((block, index) => (
				<Fragment key={index}>{renderBlock(block, ctx)}</Fragment>
			))}
		</View>
	);
});

interface RenderContext {
	colors: Palette;
	numbering: CitationNumbering;
	onCitation?: CitationHandler;
}

const HEADING_SIZES = [22, 19, 17, 16, 16, 15];

function renderBlock(block: Block, ctx: RenderContext): ReactNode {
	const { colors } = ctx;
	const base: TextStyle = { color: colors.foreground, fontSize: 16, lineHeight: 24 };
	switch (block.type) {
		case "heading":
			return (
				<Text
					selectable
					style={[
						base,
						{
							fontSize: HEADING_SIZES[block.level - 1],
							lineHeight: HEADING_SIZES[block.level - 1] * 1.35,
							fontWeight: "700",
							marginTop: 4,
						},
					]}
				>
					{renderInlines(block.inline, ctx)}
				</Text>
			);
		case "paragraph":
			return (
				<Text selectable style={base}>
					{renderInlines(block.inline, ctx)}
				</Text>
			);
		case "list": {
			let counter = 0;
			return (
				<View style={{ gap: 4 }}>
					{block.items.map((item, index) => {
						counter = item.ordered ? counter + 1 : counter;
						const marker = item.checked !== undefined ? (item.checked ? "☑" : "☐") : item.ordered ? item.marker : "•";
						return (
							<View key={index} style={{ flexDirection: "row", paddingLeft: item.level * 18, gap: 8 }}>
								<Text style={[base, { minWidth: item.ordered ? 22 : 12, color: colors.mutedForeground }]}>
									{marker}
								</Text>
								<Text selectable style={[base, { flex: 1 }]}>
									{renderInlines(item.inline, ctx)}
								</Text>
							</View>
						);
					})}
				</View>
			);
		}
		case "code":
			return (
				<ScrollView horizontal style={[styles.code, { backgroundColor: colors.muted, borderColor: colors.border }]}>
					<Text selectable style={{ fontFamily: MONO, fontSize: 13, lineHeight: 19, color: colors.foreground }}>
						{block.text}
					</Text>
				</ScrollView>
			);
		case "quote":
			return (
				<View style={[styles.quote, { borderLeftColor: colors.border }]}>
					{block.blocks.map((inner, index) => (
						<Fragment key={index}>{renderBlock(inner, ctx)}</Fragment>
					))}
				</View>
			);
		case "hr":
			return <View style={{ height: StyleSheet.hairlineWidth, backgroundColor: colors.border, marginVertical: 4 }} />;
		case "table":
			return (
				<ScrollView horizontal>
					<View style={[styles.table, { borderColor: colors.border }]}>
						{[block.header, ...block.rows].map((row, rowIndex) => (
							<View
								key={rowIndex}
								style={[
									styles.tableRow,
									{ borderColor: colors.border, backgroundColor: rowIndex === 0 ? colors.muted : "transparent" },
								]}
							>
								{row.map((cell, cellIndex) => (
									<Text
										key={cellIndex}
										selectable
										style={[
											base,
											styles.tableCell,
											{
												fontSize: 14,
												lineHeight: 20,
												fontWeight: rowIndex === 0 ? "600" : "400",
												borderColor: colors.border,
											},
										]}
									>
										{renderInlines(cell, ctx)}
									</Text>
								))}
							</View>
						))}
					</View>
				</ScrollView>
			);
	}
}

function renderInlines(nodes: readonly Inline[], ctx: RenderContext): ReactNode[] {
	return nodes.map((node, index) => renderInline(node, ctx, index));
}

function renderInline(node: Inline, ctx: RenderContext, key: number): ReactNode {
	const { colors } = ctx;
	switch (node.type) {
		case "text":
			return <Fragment key={key}>{renderCitations(node.text, ctx)}</Fragment>;
		case "strong":
			return (
				<Text key={key} style={{ fontWeight: "700" }}>
					{renderInlines(node.children, ctx)}
				</Text>
			);
		case "em":
			return (
				<Text key={key} style={{ fontStyle: "italic" }}>
					{renderInlines(node.children, ctx)}
				</Text>
			);
		case "code":
			return (
				<Text key={key} style={{ fontFamily: MONO, fontSize: 14, backgroundColor: colors.muted }}>
					{` ${node.text} `}
				</Text>
			);
		case "link":
			return (
				<Text
					key={key}
					accessibilityRole="link"
					style={{ textDecorationLine: "underline" }}
					onPress={() => {
						if (/^https?:\/\//i.test(node.href)) void WebBrowser.openBrowserAsync(node.href);
					}}
				>
					{renderInlines(node.children, ctx)}
				</Text>
			);
	}
}

function renderCitations(text: string, ctx: RenderContext): ReactNode {
	const segments = parseCitations(text);
	if (segments.length === 1 && typeof segments[0] === "string") return segments[0];
	return segments.map((segment, index) => {
		if (typeof segment === "string") return <Fragment key={index}>{segment}</Fragment>;
		const number = ctx.numbering.numberOf(segment);
		if (number === null) return null;
		return (
			<Text
				key={index}
				accessibilityRole="button"
				accessibilityLabel={`Citation ${number}`}
				onPress={ctx.onCitation ? () => ctx.onCitation?.(segment, number) : undefined}
				style={{ fontSize: 12, fontWeight: "700", color: ctx.colors.foreground, backgroundColor: ctx.colors.citation }}
			>
				{` ${number} `}
			</Text>
		);
	});
}

const styles = StyleSheet.create({
	code: { borderWidth: 1, borderRadius: radius.md, padding: space.md },
	quote: { borderLeftWidth: 3, paddingLeft: space.md, gap: space.sm },
	table: { borderWidth: 1, borderRadius: radius.sm },
	tableRow: { flexDirection: "row", borderBottomWidth: StyleSheet.hairlineWidth },
	tableCell: { width: 160, padding: space.sm, borderRightWidth: StyleSheet.hairlineWidth },
});
