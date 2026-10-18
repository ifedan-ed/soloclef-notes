import { RuntimeConfig } from "@/components/providers/runtime-config.server";

export default function ForgotPasswordLayout({ children }: { children: React.ReactNode }) {
	return <RuntimeConfig>{children}</RuntimeConfig>;
}
