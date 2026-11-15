import { createFromSource } from "fumadocs-core/search/server";
import { source } from "@/lib/source";

// The docs' search index, written once at build time as a file
// (out/api/search) and searched in the browser (the RootProvider in
// app/layout.tsx asks for it with `type: "static"`). The docs only change
// with a build, so a server answering each search would only ever have
// returned what this file already holds.
export const revalidate = false;
export const { staticGET: GET } = createFromSource(source);
