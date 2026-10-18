/**
 * Which static file-type table the upload dialog uses before the backend's
 * GET /api/v1/etl/capabilities answers (or if it cannot be reached).
 *
 * Read from the same two settings the backend reads, so the table matches
 * the parser an upload that names none gets: ETL_DEFAULT_PARSER (LOCAL,
 * the default, or CLOUD) and ETL_CLOUD_SERVICE (LLAMACLOUD, the default,
 * DATALAB or UNSTRUCTURED). The deprecated ETL_SERVICE counts only while
 * ETL_CLOUD_SERVICE is unset, as in the backend
 * (surfsense_backend/app/config/parser_settings.py). With none of the three
 * set the caller keeps its build-time fallback.
 */

export const LOCAL_ETL_SERVICE = "PYMUPDF4LLM";
const CLOUD_SERVICES = ["LLAMACLOUD", "DATALAB", "UNSTRUCTURED"];
const DEFAULT_CLOUD_SERVICE = "LLAMACLOUD";

/** The environment, or any map holding the three lines. */
type ParserEnv = Record<string, string | undefined>;

const clean = (value: string | undefined) => (value ?? "").trim().toUpperCase();

export function fallbackEtlService(env: ParserEnv): string | undefined {
	const defaultParser = clean(env.ETL_DEFAULT_PARSER);
	const cloud = clean(env.ETL_CLOUD_SERVICE);
	const legacy = clean(env.ETL_SERVICE);
	if (!defaultParser && !cloud && !legacy) return undefined;
	if (defaultParser !== "CLOUD") return LOCAL_ETL_SERVICE;
	if (cloud) return cloud;
	return CLOUD_SERVICES.includes(legacy) ? legacy : DEFAULT_CLOUD_SERVICE;
}
