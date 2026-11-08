import { useMemo, useState } from "react";
import { Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ArtifactManifest } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { type MindNode, parseMindMap } from "../../lib/markdown/mindmap";
import { radius, space } from "../../theme/colors";
import { AppText, Button, Icon, Notice } from "../ui/primitives";

function allIds(node: MindNode, into: Set<string> = new Set()): Set<string> {
	if (node.children.length > 0) into.add(node.id);
	for (const child of node.children) allIds(child, into);
	return into;
}

/**
 * The mind map as a collapsible outline (the web draws the same hierarchy
 * with markmap). The centre and its first ring start open.
 */
export function MindMapViewer({ manifest }: { manifest: ArtifactManifest }) {
	const { t } = useApp();
	const insets = useSafeAreaInsets();
	const root = useMemo(() => parseMindMap(manifest.markdown_representation, manifest.title), [manifest]);
	const [open, setOpen] = useState<Set<string>>(
		() => new Set(root ? [root.id, ...root.children.map((c) => c.id)] : [])
	);

	if (!root) {
		return (
			<View style={{ padding: space.lg }}>
				<Notice tone="error" message={t("mobile.output_failed")} />
			</View>
		);
	}
	const toggle = (id: string) =>
		setOpen((current) => {
			const next = new Set(current);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});

	return (
		<ScrollView contentContainerStyle={{ padding: space.lg, paddingBottom: insets.bottom + space.xxl, gap: space.md }}>
			<View style={{ flexDirection: "row", gap: space.sm }}>
				<Button
					small
					kind="outline"
					icon="expand-outline"
					title={t("mobile.expand_all")}
					onPress={() => setOpen(allIds(root))}
				/>
				<Button
					small
					kind="ghost"
					icon="contract-outline"
					title={t("mobile.collapse_all")}
					onPress={() => setOpen(new Set([root.id]))}
				/>
			</View>
			<Node node={root} depth={0} open={open} onToggle={toggle} />
		</ScrollView>
	);
}

function Node({
	node,
	depth,
	open,
	onToggle,
}: {
	node: MindNode;
	depth: number;
	open: Set<string>;
	onToggle: (id: string) => void;
}) {
	const { colors } = useApp();
	const expanded = open.has(node.id);
	const branch = node.children.length > 0;
	return (
		<View style={{ gap: 6 }}>
			<Pressable
				onPress={() => branch && onToggle(node.id)}
				accessibilityRole={branch ? "button" : "text"}
				accessibilityState={branch ? { expanded } : undefined}
				style={[
					styles.node,
					{
						marginLeft: depth === 0 ? 0 : 12,
						borderColor: depth === 0 ? colors.foreground : colors.border,
						backgroundColor: depth === 0 ? colors.primary : depth === 1 ? colors.muted : colors.background,
					},
				]}
			>
				{branch ? (
					<Icon
						name={expanded ? "chevron-down" : "chevron-forward"}
						size={16}
						color={depth === 0 ? colors.primaryForeground : colors.mutedForeground}
					/>
				) : (
					<View style={[styles.dot, { backgroundColor: colors.mutedForeground }]} />
				)}
				<AppText
					variant={depth === 0 ? "subheading" : depth === 1 ? "label" : "small"}
					tone={depth === 0 ? "inverse" : "default"}
					style={{ flex: 1 }}
				>
					{node.label}
				</AppText>
				{branch && !expanded ? (
					<AppText variant="caption" tone={depth === 0 ? "inverse" : "muted"}>
						{node.children.length}
					</AppText>
				) : null}
			</Pressable>
			{expanded ? (
				<View style={[styles.children, { borderLeftColor: colors.border, marginLeft: depth === 0 ? 10 : 22 }]}>
					{node.children.map((child) => (
						<Node key={child.id} node={child} depth={depth + 1} open={open} onToggle={onToggle} />
					))}
				</View>
			) : null}
		</View>
	);
}

const styles = StyleSheet.create({
	node: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		borderWidth: 1,
		borderRadius: radius.md,
		paddingHorizontal: space.md,
		paddingVertical: 10,
	},
	dot: { width: 6, height: 6, borderRadius: 3, marginHorizontal: 5 },
	children: { borderLeftWidth: StyleSheet.hairlineWidth, paddingLeft: 4, gap: 6 },
});
