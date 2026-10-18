import { SiteShell } from "@/components/site/site-shell";
import "./home.css";

/** Layout for every public route: the landing, features, pricing, legal and auth pages. */
export default function HomePageLayout({ children }: { children: React.ReactNode }) {
	return <SiteShell>{children}</SiteShell>;
}
