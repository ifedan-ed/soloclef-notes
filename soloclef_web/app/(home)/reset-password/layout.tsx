import { RuntimeConfig } from "@/components/providers/runtime-config.server";

export default function ResetPasswordLayout({ children }: { children: React.ReactNode }) {
	return <RuntimeConfig>{children}</RuntimeConfig>;
}
