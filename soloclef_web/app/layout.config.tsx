import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import Image from "next/image";
import brand from "@/lib/brand/brand.json";

export const baseOptions: BaseLayoutProps = {
	nav: {
		title: (
			<>
				<Image src={brand.logo.mark} alt="" width={24} height={24} className="rounded-md" />
				{brand.name} Docs
			</>
		),
	},
	themeSwitch: { enabled: false },
};
