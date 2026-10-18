import { Ionicons } from "@expo/vector-icons";
import { type ReactNode, createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { Animated, KeyboardAvoidingView, Modal, Platform, Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useApp } from "../../lib/app-context";
import { radius, space } from "../../theme/colors";
import { AppText, IconButton } from "./primitives";

/**
 * Top bar drawn inside the safe area (the web just had headers slipping under
 * the iOS status bar; here the inset is always applied).
 */
export function Header({
	title,
	subtitle,
	onBack,
	backLabel,
	left,
	right,
}: {
	title?: ReactNode;
	subtitle?: string;
	onBack?: () => void;
	backLabel?: string;
	left?: ReactNode;
	right?: ReactNode;
}) {
	const { colors } = useApp();
	const insets = useSafeAreaInsets();
	return (
		<View
			style={[
				styles.header,
				{
					paddingTop: insets.top + 6,
					paddingLeft: insets.left + space.sm,
					paddingRight: insets.right + space.sm,
					borderBottomColor: colors.border,
					backgroundColor: colors.background,
				},
			]}
		>
			{onBack ? <IconButton name="chevron-back" label={backLabel ?? "Back"} onPress={onBack} /> : null}
			{left}
			<View style={{ flex: 1, minWidth: 0, paddingHorizontal: onBack || left ? 0 : space.sm }}>
				{typeof title === "string" ? (
					<AppText variant="subheading" numberOfLines={1}>
						{title}
					</AppText>
				) : (
					title
				)}
				{subtitle ? (
					<AppText variant="caption" tone="muted" numberOfLines={1}>
						{subtitle}
					</AppText>
				) : null}
			</View>
			{right ? <View style={styles.headerRight}>{right}</View> : null}
		</View>
	);
}

export function Screen({ children }: { children: ReactNode }) {
	const { colors } = useApp();
	return <View style={{ flex: 1, backgroundColor: colors.background }}>{children}</View>;
}

/**
 * A bottom sheet in an in-app modal (not a system dialog). Content scrolls,
 * the keyboard pushes it up, and the home indicator inset is respected.
 */
export function Sheet({
	visible,
	onClose,
	title,
	children,
	footer,
	full,
	scroll = true,
}: {
	visible: boolean;
	onClose: () => void;
	title?: string;
	children: ReactNode;
	footer?: ReactNode;
	/** Take most of the screen (long forms). */
	full?: boolean;
	scroll?: boolean;
}) {
	const { colors, t } = useApp();
	const insets = useSafeAreaInsets();
	return (
		<Modal
			visible={visible}
			transparent
			animationType="slide"
			onRequestClose={onClose}
			statusBarTranslucent
			navigationBarTranslucent
		>
			<KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : "height"} style={{ flex: 1 }}>
				<Pressable
					style={[StyleSheet.absoluteFill, { backgroundColor: colors.overlay }]}
					onPress={onClose}
					accessibilityLabel={t("common.close")}
				/>
				<View style={{ flex: 1 }} pointerEvents="box-none" />
				<View
					style={[
						styles.sheet,
						{
							backgroundColor: colors.background,
							borderColor: colors.border,
							paddingBottom: insets.bottom + space.md,
							maxHeight: "92%",
						},
						full && { height: "92%" },
					]}
				>
					<View style={styles.grabber}>
						<View style={[styles.grabberBar, { backgroundColor: colors.border }]} />
					</View>
					{title ? (
						<View style={styles.sheetHeader}>
							<AppText variant="heading" style={{ flex: 1 }} numberOfLines={2}>
								{title}
							</AppText>
							<IconButton name="close" label={t("common.close")} onPress={onClose} />
						</View>
					) : null}
					{scroll ? (
						<ScrollView
							keyboardShouldPersistTaps="handled"
							contentContainerStyle={styles.sheetBody}
							style={full ? { flex: 1 } : undefined}
						>
							{children}
						</ScrollView>
					) : (
						<View style={[styles.sheetBody, full && { flex: 1 }]}>{children}</View>
					)}
					{footer ? <View style={[styles.sheetFooter, { borderTopColor: colors.border }]}>{footer}</View> : null}
				</View>
			</KeyboardAvoidingView>
		</Modal>
	);
}

// --- Toasts: brief, non-blocking notes over the content -------------------

type Toast = { id: number; message: string; tone: "info" | "error" | "success" };
const ToastContext = createContext<(message: string, tone?: Toast["tone"]) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
	const [toast, setToast] = useState<Toast | null>(null);
	const [opacity] = useState(() => new Animated.Value(0));
	const counter = useRef(0);
	const show = useCallback((message: string, tone: Toast["tone"] = "info") => {
		counter.current += 1;
		setToast({ id: counter.current, message, tone });
	}, []);
	useEffect(() => {
		if (!toast) return;
		Animated.timing(opacity, { toValue: 1, duration: 150, useNativeDriver: true }).start();
		const timer = setTimeout(() => {
			Animated.timing(opacity, { toValue: 0, duration: 200, useNativeDriver: true }).start(() =>
				setToast((current) => (current?.id === toast.id ? null : current))
			);
		}, 3200);
		return () => clearTimeout(timer);
	}, [toast, opacity]);
	return (
		<ToastContext.Provider value={show}>
			{children}
			{toast ? <ToastView toast={toast} opacity={opacity} onDismiss={() => setToast(null)} /> : null}
		</ToastContext.Provider>
	);
}

function ToastView({ toast, opacity, onDismiss }: { toast: Toast; opacity: Animated.Value; onDismiss: () => void }) {
	const { colors } = useApp();
	const insets = useSafeAreaInsets();
	return (
		<Animated.View pointerEvents="box-none" style={[styles.toastWrap, { bottom: insets.bottom + 84, opacity }]}>
			<Pressable
				onPress={onDismiss}
				accessibilityRole="alert"
				style={[styles.toast, { backgroundColor: colors.primary }]}
			>
				<Ionicons
					name={
						toast.tone === "error"
							? "alert-circle"
							: toast.tone === "success"
								? "checkmark-circle"
								: "information-circle"
					}
					size={18}
					color={toast.tone === "error" ? colors.destructive : colors.primaryForeground}
				/>
				<AppText variant="small" tone="inverse" style={{ flex: 1 }}>
					{toast.message}
				</AppText>
			</Pressable>
		</Animated.View>
	);
}

export function useToast() {
	return useContext(ToastContext);
}

const styles = StyleSheet.create({
	header: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.xs,
		paddingBottom: 6,
		borderBottomWidth: StyleSheet.hairlineWidth,
		minHeight: 52,
	},
	headerRight: { flexDirection: "row", alignItems: "center" },
	sheet: { borderTopLeftRadius: radius.xl, borderTopRightRadius: radius.xl, borderWidth: StyleSheet.hairlineWidth },
	grabber: { alignItems: "center", paddingTop: space.sm },
	grabberBar: { width: 40, height: 4, borderRadius: 2 },
	sheetHeader: {
		flexDirection: "row",
		alignItems: "center",
		paddingLeft: space.lg,
		paddingRight: space.sm,
		paddingTop: space.xs,
	},
	sheetBody: { padding: space.lg, gap: space.lg },
	sheetFooter: {
		paddingHorizontal: space.lg,
		paddingTop: space.md,
		borderTopWidth: StyleSheet.hairlineWidth,
		flexDirection: "row",
		gap: space.sm,
		justifyContent: "flex-end",
	},
	toastWrap: { position: "absolute", left: space.lg, right: space.lg, alignItems: "center" },
	toast: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		borderRadius: radius.md,
		paddingHorizontal: space.lg,
		paddingVertical: space.md,
		maxWidth: 520,
		width: "100%",
	},
});
