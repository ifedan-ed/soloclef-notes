/**
 * The app's small UI kit, drawn after the web's shadcn components: neutral
 * palette, rounded pills, 16 px inputs (no iOS zoom, readable on phones) and
 * 44 pt touch targets.
 */
import { Ionicons } from "@expo/vector-icons";
import { type ComponentProps, forwardRef, type ReactNode, useEffect, useState } from "react";
import {
	ActivityIndicator,
	Pressable,
	type PressableProps,
	type StyleProp,
	StyleSheet,
	Text,
	type TextProps,
	TextInput,
	type TextInputProps,
	type TextStyle,
	View,
	type ViewStyle,
} from "react-native";
import { useApp } from "../../lib/app-context";
import { radius, space } from "../../theme/colors";

export type IconName = ComponentProps<typeof Ionicons>["name"];

type Variant = "title" | "heading" | "subheading" | "body" | "small" | "caption" | "label";
type Tone = "default" | "muted" | "destructive" | "inverse" | "success";

const VARIANTS: Record<Variant, TextStyle> = {
	title: { fontSize: 24, fontWeight: "700", lineHeight: 30 },
	heading: { fontSize: 18, fontWeight: "600", lineHeight: 24 },
	subheading: { fontSize: 16, fontWeight: "600", lineHeight: 22 },
	body: { fontSize: 16, lineHeight: 23 },
	small: { fontSize: 14, lineHeight: 20 },
	caption: { fontSize: 12, lineHeight: 16 },
	label: { fontSize: 14, fontWeight: "600", lineHeight: 20 },
};

export function AppText({
	variant = "body",
	tone = "default",
	style,
	...props
}: TextProps & { variant?: Variant; tone?: Tone }) {
	const { colors } = useApp();
	const color = {
		default: colors.foreground,
		muted: colors.mutedForeground,
		destructive: colors.destructive,
		inverse: colors.primaryForeground,
		success: colors.success,
	}[tone];
	return <Text {...props} style={[VARIANTS[variant], { color }, style]} />;
}

export function Icon({
	name,
	size = 20,
	color,
	style,
}: {
	name: IconName;
	size?: number;
	color?: string;
	style?: StyleProp<TextStyle>;
}) {
	const { colors } = useApp();
	return <Ionicons name={name} size={size} color={color ?? colors.foreground} style={style} />;
}

type ButtonKind = "primary" | "secondary" | "outline" | "ghost" | "destructive";

export function Button({
	title,
	onPress,
	kind = "primary",
	icon,
	disabled,
	busy,
	small,
	style,
	accessibilityLabel,
}: {
	title: string;
	onPress?: () => void;
	kind?: ButtonKind;
	icon?: IconName;
	disabled?: boolean;
	busy?: boolean;
	small?: boolean;
	style?: StyleProp<ViewStyle>;
	accessibilityLabel?: string;
}) {
	const { colors } = useApp();
	const palette: Record<ButtonKind, { bg: string; fg: string; border: string }> = {
		primary: { bg: colors.primary, fg: colors.primaryForeground, border: colors.primary },
		secondary: { bg: colors.secondary, fg: colors.foreground, border: colors.secondary },
		outline: { bg: "transparent", fg: colors.foreground, border: colors.border },
		ghost: { bg: "transparent", fg: colors.foreground, border: "transparent" },
		destructive: { bg: colors.destructive, fg: colors.destructiveForeground, border: colors.destructive },
	};
	const p = palette[kind];
	const showBusy = useDelayedFlag(!!busy);
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel ?? title}
			accessibilityState={{ disabled: !!disabled || !!busy, busy: !!busy }}
			disabled={disabled || busy}
			onPress={onPress}
			style={({ pressed }) => [
				styles.button,
				small && styles.buttonSmall,
				{ backgroundColor: p.bg, borderColor: p.border, opacity: disabled ? 0.5 : pressed ? 0.8 : 1 },
				style,
			]}
		>
			{showBusy ? (
				<ActivityIndicator size="small" color={p.fg} />
			) : icon ? (
				<Ionicons name={icon} size={small ? 16 : 18} color={p.fg} />
			) : null}
			<Text numberOfLines={1} style={[small ? VARIANTS.small : VARIANTS.label, { color: p.fg, fontWeight: "600" }]}>
				{title}
			</Text>
		</Pressable>
	);
}

export function IconButton({
	name,
	onPress,
	label,
	size = 22,
	color,
	disabled,
	style,
}: {
	name: IconName;
	onPress?: () => void;
	label: string;
	size?: number;
	color?: string;
	disabled?: boolean;
	style?: StyleProp<ViewStyle>;
}) {
	const { colors } = useApp();
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={label}
			hitSlop={6}
			disabled={disabled}
			onPress={onPress}
			style={({ pressed }) => [
				styles.iconButton,
				{ backgroundColor: pressed ? colors.accent : "transparent", opacity: disabled ? 0.4 : 1 },
				style,
			]}
		>
			<Ionicons name={name} size={size} color={color ?? colors.foreground} />
		</Pressable>
	);
}

export const TextField = forwardRef<TextInput, TextInputProps & { invalid?: boolean }>(function TextField(
	{ style, invalid, multiline, ...props },
	ref
) {
	const { colors, scheme } = useApp();
	return (
		<TextInput
			ref={ref}
			placeholderTextColor={colors.mutedForeground}
			keyboardAppearance={scheme}
			multiline={multiline}
			{...props}
			style={[
				styles.input,
				multiline && styles.inputMultiline,
				{
					color: colors.foreground,
					borderColor: invalid ? colors.destructive : colors.input,
					backgroundColor: colors.background,
				},
				style,
			]}
		/>
	);
});

/** One-line pills; the whole row scrolls sideways only if it cannot fit. */
export function Segmented<T extends string>({
	value,
	options,
	onChange,
	accessibilityLabel,
}: {
	value: T;
	options: readonly { value: T; label: string; accessibilityLabel?: string }[];
	onChange: (value: T) => void;
	accessibilityLabel?: string;
}) {
	const { colors } = useApp();
	return (
		<View
			accessibilityRole="radiogroup"
			accessibilityLabel={accessibilityLabel}
			style={[styles.segmented, { backgroundColor: colors.muted }]}
		>
			{options.map((option) => {
				const selected = option.value === value;
				return (
					<Pressable
						key={option.value}
						accessibilityRole="radio"
						accessibilityState={{ selected }}
						accessibilityLabel={option.accessibilityLabel ?? option.label}
						onPress={() => onChange(option.value)}
						style={[styles.segment, selected && { backgroundColor: colors.background, ...styles.segmentShadow }]}
					>
						<Text
							numberOfLines={1}
							adjustsFontSizeToFit
							minimumFontScale={0.8}
							style={[
								VARIANTS.small,
								{ color: selected ? colors.foreground : colors.mutedForeground, fontWeight: selected ? "600" : "500" },
							]}
						>
							{option.label}
						</Text>
					</Pressable>
				);
			})}
		</View>
	);
}

/** A described choice (the web's option cards). */
export function OptionCard({
	title,
	description,
	selected,
	onPress,
}: {
	title: string;
	description?: string;
	selected: boolean;
	onPress: () => void;
}) {
	const { colors } = useApp();
	return (
		<Pressable
			accessibilityRole="radio"
			accessibilityState={{ selected }}
			onPress={onPress}
			style={[
				styles.optionCard,
				{
					borderColor: selected ? colors.foreground : colors.border,
					backgroundColor: selected ? colors.accent : colors.background,
				},
			]}
		>
			<View style={{ flex: 1, gap: 2 }}>
				<AppText variant="label">{title}</AppText>
				{description ? (
					<AppText variant="caption" tone="muted">
						{description}
					</AppText>
				) : null}
			</View>
			<Ionicons
				name={selected ? "radio-button-on" : "radio-button-off"}
				size={20}
				color={selected ? colors.foreground : colors.mutedForeground}
			/>
		</Pressable>
	);
}

export function Chip({
	label,
	onPress,
	icon,
	onRemove,
	removeLabel,
}: {
	label: string;
	onPress?: () => void;
	icon?: IconName;
	onRemove?: () => void;
	removeLabel?: string;
}) {
	const { colors } = useApp();
	// The remove button is a sibling of the pressable label, never nested in it.
	return (
		<View style={[styles.chip, { borderColor: colors.border, backgroundColor: colors.background }]}>
			<Pressable
				disabled={!onPress}
				onPress={onPress}
				accessibilityRole={onPress ? "button" : undefined}
				accessibilityLabel={label}
				style={({ pressed }) => [styles.chipBody, { opacity: pressed ? 0.6 : 1 }]}
			>
				{icon ? <Ionicons name={icon} size={14} color={colors.mutedForeground} /> : null}
				<Text style={[VARIANTS.small, { color: colors.foreground, flexShrink: 1 }]} numberOfLines={2}>
					{label}
				</Text>
			</Pressable>
			{onRemove ? (
				<Pressable onPress={onRemove} hitSlop={8} accessibilityRole="button" accessibilityLabel={removeLabel ?? label}>
					<Ionicons name="close" size={14} color={colors.mutedForeground} />
				</Pressable>
			) : null}
		</View>
	);
}

export function Card({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
	const { colors } = useApp();
	return (
		<View style={[styles.card, { borderColor: colors.border, backgroundColor: colors.card }, style]}>{children}</View>
	);
}

export function Divider() {
	const { colors } = useApp();
	return <View style={{ height: StyleSheet.hairlineWidth, backgroundColor: colors.border }} />;
}

/** Inline message (never a system popup): errors, notes, confirmations. */
export function Notice({
	tone = "info",
	message,
	actionLabel,
	onAction,
	onDismiss,
}: {
	tone?: "info" | "error" | "success";
	message: string;
	actionLabel?: string;
	onAction?: () => void;
	onDismiss?: () => void;
}) {
	const { colors } = useApp();
	const fg = tone === "error" ? colors.destructive : tone === "success" ? colors.success : colors.foreground;
	return (
		<View
			accessibilityRole="alert"
			style={[
				styles.notice,
				{ borderColor: tone === "error" ? colors.destructive : colors.border, backgroundColor: colors.muted },
			]}
		>
			<Ionicons
				name={tone === "error" ? "alert-circle" : tone === "success" ? "checkmark-circle" : "information-circle"}
				size={18}
				color={fg}
			/>
			<AppText variant="small" style={{ flex: 1, color: fg }}>
				{message}
			</AppText>
			{actionLabel && onAction ? (
				<Pressable onPress={onAction} hitSlop={8} accessibilityRole="button">
					<AppText variant="label">{actionLabel}</AppText>
				</Pressable>
			) : null}
			{onDismiss ? (
				<Pressable onPress={onDismiss} hitSlop={8} accessibilityRole="button" accessibilityLabel="Dismiss">
					<Ionicons name="close" size={16} color={colors.mutedForeground} />
				</Pressable>
			) : null}
		</View>
	);
}

/**
 * True only after ``flag`` has been true for ``delay`` ms, then held for at
 * least ``minVisible`` ms: a fast load shows nothing, a slow one shows a
 * loader that does not flicker out mid-frame.
 */
export function useDelayedFlag(flag: boolean, delay = 250, minVisible = 300): boolean {
	const [visible, setVisible] = useState(false);
	const [shownAt, setShownAt] = useState(0);
	useEffect(() => {
		if (flag) {
			const timer = setTimeout(() => {
				setVisible(true);
				setShownAt(Date.now());
			}, delay);
			return () => clearTimeout(timer);
		}
		if (!visible) return;
		const remaining = Math.max(0, minVisible - (Date.now() - shownAt));
		const timer = setTimeout(() => setVisible(false), remaining);
		return () => clearTimeout(timer);
	}, [flag, delay, minVisible, visible, shownAt]);
	return visible;
}

export function DelayedSpinner({ active, style }: { active: boolean; style?: StyleProp<ViewStyle> }) {
	const { colors } = useApp();
	const show = useDelayedFlag(active);
	if (!show) return null;
	return <ActivityIndicator color={colors.mutedForeground} style={style} />;
}

export function Checkbox({ checked, onPress, label }: { checked: boolean; onPress: () => void; label: string }) {
	const { colors } = useApp();
	return (
		<Pressable
			accessibilityRole="checkbox"
			accessibilityState={{ checked }}
			accessibilityLabel={label}
			onPress={onPress}
			hitSlop={8}
		>
			<Ionicons
				name={checked ? "checkbox" : "square-outline"}
				size={22}
				color={checked ? colors.foreground : colors.mutedForeground}
			/>
		</Pressable>
	);
}

export function PressableRow({
	children,
	style,
	...props
}: PressableProps & { style?: StyleProp<ViewStyle>; children: ReactNode }) {
	const { colors } = useApp();
	return (
		<Pressable
			{...props}
			style={({ pressed }) => [styles.row, { backgroundColor: pressed ? colors.accent : "transparent" }, style]}
		>
			{children}
		</Pressable>
	);
}

const styles = StyleSheet.create({
	button: {
		minHeight: 44,
		paddingHorizontal: space.lg,
		borderRadius: radius.md,
		borderWidth: 1,
		flexDirection: "row",
		alignItems: "center",
		justifyContent: "center",
		gap: space.sm,
	},
	buttonSmall: { minHeight: 36, paddingHorizontal: space.md },
	iconButton: { width: 40, height: 40, borderRadius: 20, alignItems: "center", justifyContent: "center" },
	input: {
		fontSize: 16,
		minHeight: 44,
		borderWidth: 1,
		borderRadius: radius.md,
		paddingHorizontal: space.md,
		paddingVertical: 10,
	},
	inputMultiline: { minHeight: 88, textAlignVertical: "top" },
	segmented: { flexDirection: "row", borderRadius: radius.pill, padding: 3, gap: 2 },
	segment: {
		flex: 1,
		minHeight: 36,
		borderRadius: radius.pill,
		alignItems: "center",
		justifyContent: "center",
		paddingHorizontal: space.sm,
	},
	segmentShadow: {
		shadowColor: "#000",
		shadowOpacity: 0.1,
		shadowRadius: 2,
		shadowOffset: { width: 0, height: 1 },
		elevation: 1,
	},
	optionCard: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.md,
		borderWidth: 1,
		borderRadius: radius.md,
		padding: space.md,
	},
	chip: {
		flexDirection: "row",
		alignItems: "center",
		gap: 6,
		borderWidth: 1,
		borderRadius: radius.pill,
		paddingHorizontal: space.md,
		paddingVertical: 8,
		maxWidth: "100%",
	},
	chipBody: { flexDirection: "row", alignItems: "center", gap: 6, flexShrink: 1 },
	card: { borderWidth: 1, borderRadius: radius.lg, padding: space.lg },
	notice: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		borderWidth: 1,
		borderRadius: radius.md,
		padding: space.md,
	},
	row: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.md,
		paddingHorizontal: space.lg,
		paddingVertical: space.md,
		minHeight: 52,
	},
});
