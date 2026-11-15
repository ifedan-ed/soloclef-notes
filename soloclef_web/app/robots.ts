import type { MetadataRoute } from "next";
import { robotsRules } from "@/lib/seo/robots";

// Written once, at build time, into the static site.
export const dynamic = "force-static";

export default function robots(): MetadataRoute.Robots {
	return robotsRules();
}
