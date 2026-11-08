import defaultMdxComponents from "fumadocs-ui/mdx";
import type { MDXComponents } from "mdx/types";
import Image, { type ImageProps } from "next/image";
import { BrandName } from "@/components/docs/brand-name";
import { CreditCostTable, PackTable, PlanTable } from "@/components/docs/pricing-tables";
import {
	SsoAccountsFor,
	SsoButtonLabel,
	SsoDomains,
	SsoOnly,
	SsoProviderHost,
} from "@/components/docs/single-sign-on";
import { SiteAddress } from "@/components/docs/site-address";
import {
	Accordion,
	AccordionContent,
	AccordionItem,
	AccordionTrigger,
} from "@/components/ui/accordion";
import { cn } from "@/lib/utils";

export function getMDXComponents(components?: MDXComponents): MDXComponents {
	return {
		...defaultMdxComponents,
		img: ({ className, alt, ...props }: React.ComponentProps<"img">) => (
			<Image
				{...(props as ImageProps)}
				alt={alt ?? ""}
				sizes="(max-width: 768px) 100vw, 896px"
				className={cn("rounded-md border", className)}
			/>
		),
		Video: ({ className, ...props }: React.ComponentProps<"video">) => (
			<video className={cn("rounded-md border", className)} controls loop {...props} />
		),
		Accordion,
		AccordionItem,
		AccordionTrigger,
		AccordionContent,
		BrandName,
		PlanTable,
		PackTable,
		CreditCostTable,
		// This server's own address and single sign-on, never one server's names.
		SiteAddress,
		SsoAccountsFor,
		SsoButtonLabel,
		SsoDomains,
		SsoOnly,
		SsoProviderHost,
		...components,
	};
}

export const useMDXComponents = getMDXComponents;
