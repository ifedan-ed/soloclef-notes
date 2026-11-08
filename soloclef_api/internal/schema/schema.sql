--
-- PostgreSQL database dump
--



SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: public; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA public;


--
-- Name: artifact_file_role; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.artifact_file_role AS ENUM (
    'primary',
    'preview'
);


--
-- Name: automation_run_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.automation_run_status AS ENUM (
    'pending',
    'running',
    'succeeded',
    'failed',
    'needs_attention',
    'cancelled',
    'timed_out'
);


--
-- Name: automation_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.automation_status AS ENUM (
    'active',
    'paused',
    'archived'
);


--
-- Name: automation_trigger_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.automation_trigger_type AS ENUM (
    'schedule',
    'event',
    'manual'
);


--
-- Name: chatvisibility; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.chatvisibility AS ENUM (
    'PRIVATE',
    'SEARCH_SPACE'
);


--
-- Name: connectionscope; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.connectionscope AS ENUM (
    'GLOBAL',
    'SEARCH_SPACE',
    'USER'
);


--
-- Name: deliverable_job_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.deliverable_job_status AS ENUM (
    'queued',
    'running',
    'cancelling',
    'cancelled',
    'ready',
    'failed'
);


--
-- Name: document_file_kind; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.document_file_kind AS ENUM (
    'ORIGINAL',
    'REDACTED',
    'FILLED_FORM'
);


--
-- Name: documenttype; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.documenttype AS ENUM (
    'EXTENSION',
    'CRAWLED_URL',
    'FILE',
    'YOUTUBE_VIDEO',
    'GOOGLE_DRIVE_FILE',
    'NOTE',
    'ARTIFACT',
    'LOCAL_FOLDER_FILE'
);


--
-- Name: loglevel; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.loglevel AS ENUM (
    'DEBUG',
    'INFO',
    'WARNING',
    'ERROR',
    'CRITICAL'
);


--
-- Name: logstatus; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.logstatus AS ENUM (
    'IN_PROGRESS',
    'SUCCESS',
    'FAILED'
);


--
-- Name: modelsource; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.modelsource AS ENUM (
    'DISCOVERED',
    'MANUAL'
);


--
-- Name: newchatmessagerole; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.newchatmessagerole AS ENUM (
    'USER',
    'ASSISTANT',
    'SYSTEM'
);


--
-- Name: podcast_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.podcast_status AS ENUM (
    'pending',
    'awaiting_brief',
    'drafting',
    'awaiting_review',
    'rendering',
    'ready',
    'failed',
    'cancelled'
);


--
-- Name: prompt_mode; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.prompt_mode AS ENUM (
    'transform',
    'explore'
);


--
-- Name: searchsourceconnectortype; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.searchsourceconnectortype AS ENUM (
    'GOOGLE_DRIVE_CONNECTOR',
    'WEBCRAWLER_CONNECTOR',
    'MCP_CONNECTOR',
    'NEXTCLOUD_CONNECTOR',
    'EMAIL_IMAP_CONNECTOR'
);


--
-- Name: video_presentation_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.video_presentation_status AS ENUM (
    'pending',
    'generating',
    'ready',
    'failed'
);


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: agent_action_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_action_log (
    thread_id integer NOT NULL,
    user_id uuid,
    workspace_id integer NOT NULL,
    turn_id text,
    tool_call_id text,
    chat_turn_id character varying(64),
    message_id text,
    tool_name character varying(255) NOT NULL,
    args jsonb,
    result_id character varying(255),
    reversible boolean DEFAULT false NOT NULL,
    reverse_descriptor jsonb,
    error jsonb,
    reverse_of integer,
    created_at timestamp with time zone DEFAULT (now() AT TIME ZONE 'utc'::text) NOT NULL,
    id integer NOT NULL
);


--
-- Name: agent_action_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.agent_action_log_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: agent_action_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.agent_action_log_id_seq OWNED BY public.agent_action_log.id;


--
-- Name: agent_permission_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_permission_rules (
    workspace_id integer NOT NULL,
    user_id uuid,
    thread_id integer,
    permission character varying(255) NOT NULL,
    pattern character varying(255) DEFAULT '*'::character varying NOT NULL,
    action character varying(16) NOT NULL,
    created_at timestamp with time zone DEFAULT (now() AT TIME ZONE 'utc'::text) NOT NULL,
    id integer NOT NULL
);


--
-- Name: agent_permission_rules_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.agent_permission_rules_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: agent_permission_rules_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.agent_permission_rules_id_seq OWNED BY public.agent_permission_rules.id;


--
-- Name: alembic_version; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alembic_version (
    version_num character varying(32) NOT NULL
);


--
-- Name: artifact_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifact_files (
    artifact_id integer NOT NULL,
    role public.artifact_file_role NOT NULL,
    storage_backend character varying(32) NOT NULL,
    storage_key character varying NOT NULL,
    original_filename character varying NOT NULL,
    mime_type character varying NOT NULL,
    size_bytes bigint NOT NULL,
    checksum_sha256 character varying(64) NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_artifact_files_size_positive CHECK ((size_bytes > 0))
);


--
-- Name: artifact_files_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.artifact_files_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: artifact_files_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.artifact_files_id_seq OWNED BY public.artifact_files.id;


--
-- Name: artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifacts (
    document_id integer NOT NULL,
    workspace_id integer NOT NULL,
    thread_id integer,
    created_by_id uuid,
    format character varying NOT NULL,
    generation integer DEFAULT 1 NOT NULL,
    created_by_tool_call_id text,
    updated_by_tool_call_id text,
    metadata jsonb,
    updated_at timestamp with time zone NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_artifacts_generation_positive CHECK ((generation > 0))
);


--
-- Name: artifacts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.artifacts_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: artifacts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.artifacts_id_seq OWNED BY public.artifacts.id;


--
-- Name: automation_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.automation_runs (
    automation_id integer NOT NULL,
    trigger_id integer,
    status public.automation_run_status DEFAULT 'pending'::public.automation_run_status NOT NULL,
    definition_snapshot jsonb NOT NULL,
    inputs jsonb DEFAULT '{}'::jsonb NOT NULL,
    step_results jsonb DEFAULT '[]'::jsonb NOT NULL,
    output jsonb,
    artifacts jsonb DEFAULT '[]'::jsonb NOT NULL,
    error jsonb,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: automation_runs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.automation_runs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: automation_runs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.automation_runs_id_seq OWNED BY public.automation_runs.id;


--
-- Name: automation_triggers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.automation_triggers (
    automation_id integer NOT NULL,
    type public.automation_trigger_type NOT NULL,
    params jsonb NOT NULL,
    static_inputs jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_fired_at timestamp with time zone,
    next_fire_at timestamp with time zone,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: automation_triggers_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.automation_triggers_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: automation_triggers_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.automation_triggers_id_seq OWNED BY public.automation_triggers.id;


--
-- Name: automations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.automations (
    workspace_id integer NOT NULL,
    created_by_user_id uuid,
    name character varying(200) NOT NULL,
    description text,
    status public.automation_status DEFAULT 'active'::public.automation_status NOT NULL,
    definition jsonb NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: automations_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.automations_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: automations_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.automations_id_seq OWNED BY public.automations.id;


--
-- Name: billing_accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_accounts (
    user_id uuid NOT NULL,
    plan_id integer NOT NULL,
    plan_source character varying(16) NOT NULL,
    plan_status character varying(16) NOT NULL,
    period_start timestamp with time zone NOT NULL,
    period_end timestamp with time zone NOT NULL,
    plan_paid_until timestamp with time zone,
    plan_credits_micros bigint NOT NULL,
    reserved_micros bigint NOT NULL,
    exempt boolean DEFAULT false NOT NULL,
    exempt_reason text,
    stripe_subscription_id character varying(255),
    cancel_at_period_end boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: billing_action_prices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_action_prices (
    action character varying(64) NOT NULL,
    micros bigint DEFAULT '0'::bigint NOT NULL,
    unit character varying(32) DEFAULT 'action'::character varying NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    content jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: billing_ledger; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_ledger (
    id bigint NOT NULL,
    user_id uuid NOT NULL,
    bucket character varying(8) NOT NULL,
    delta_micros bigint NOT NULL,
    balance_after_micros bigint NOT NULL,
    kind character varying(24) NOT NULL,
    reason text,
    reference character varying(255),
    usage_id bigint,
    actor_user_id uuid,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: billing_ledger_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.billing_ledger_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: billing_ledger_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.billing_ledger_id_seq OWNED BY public.billing_ledger.id;


--
-- Name: billing_packs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_packs (
    id integer NOT NULL,
    slug character varying(64) NOT NULL,
    active boolean DEFAULT true NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    credits_micros bigint NOT NULL,
    price_cents integer NOT NULL,
    currency character varying(8) DEFAULT 'usd'::character varying NOT NULL,
    content jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: billing_packs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.billing_packs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: billing_packs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.billing_packs_id_seq OWNED BY public.billing_packs.id;


--
-- Name: billing_payments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_payments (
    id bigint NOT NULL,
    user_id uuid NOT NULL,
    provider character varying(16) NOT NULL,
    kind character varying(16) NOT NULL,
    provider_ref character varying(255) NOT NULL,
    status character varying(12) NOT NULL,
    amount_cents integer NOT NULL,
    currency character varying(8) NOT NULL,
    credits_micros bigint NOT NULL,
    plan_id integer,
    months integer,
    pack_id integer,
    checkout_url text,
    receipt_url text,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone
);


--
-- Name: billing_payments_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.billing_payments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: billing_payments_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.billing_payments_id_seq OWNED BY public.billing_payments.id;


--
-- Name: billing_plans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_plans (
    id integer NOT NULL,
    slug character varying(64) NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    active boolean DEFAULT true NOT NULL,
    public boolean DEFAULT true NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    price_cents integer DEFAULT 0 NOT NULL,
    currency character varying(8) DEFAULT 'usd'::character varying NOT NULL,
    monthly_credits_micros bigint DEFAULT '0'::bigint NOT NULL,
    entitlements jsonb DEFAULT '{}'::jsonb NOT NULL,
    content jsonb DEFAULT '{}'::jsonb NOT NULL,
    stripe_price_id character varying(255),
    stripe_price_cents integer,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: billing_plans_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.billing_plans_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: billing_plans_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.billing_plans_id_seq OWNED BY public.billing_plans.id;


--
-- Name: billing_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_settings (
    key character varying(128) NOT NULL,
    value jsonb,
    secret boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    updated_by uuid
);


--
-- Name: billing_usage; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_usage (
    id bigint NOT NULL,
    user_id uuid NOT NULL,
    payer_user_id uuid NOT NULL,
    workspace_id integer,
    notebook_id integer,
    thread_id integer,
    action character varying(64) NOT NULL,
    units integer NOT NULL,
    unit_micros bigint NOT NULL,
    reserved_micros bigint NOT NULL,
    charged_micros bigint NOT NULL,
    mode character varying(8) NOT NULL,
    status character varying(10) NOT NULL,
    denial_code character varying(48),
    reference character varying(255),
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    settled_at timestamp with time zone
);


--
-- Name: billing_usage_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.billing_usage_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: billing_usage_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.billing_usage_id_seq OWNED BY public.billing_usage.id;


--
-- Name: billing_webhook_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.billing_webhook_events (
    provider character varying(16) NOT NULL,
    event_id character varying(255) NOT NULL,
    event_type character varying(128),
    received_at timestamp with time zone NOT NULL
);


--
-- Name: chat_comment_mentions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chat_comment_mentions (
    comment_id integer NOT NULL,
    mentioned_user_id uuid NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: chat_comment_mentions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.chat_comment_mentions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: chat_comment_mentions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.chat_comment_mentions_id_seq OWNED BY public.chat_comment_mentions.id;


--
-- Name: chat_comments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chat_comments (
    message_id integer NOT NULL,
    thread_id integer NOT NULL,
    parent_id integer,
    author_id uuid,
    content text NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: chat_comments_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.chat_comments_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: chat_comments_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.chat_comments_id_seq OWNED BY public.chat_comments.id;


--
-- Name: chat_session_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chat_session_state (
    thread_id integer NOT NULL,
    ai_responding_to_user_id uuid,
    updated_at timestamp with time zone NOT NULL,
    id integer NOT NULL
);


--
-- Name: chat_session_state_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.chat_session_state_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: chat_session_state_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.chat_session_state_id_seq OWNED BY public.chat_session_state.id;


--
-- Name: chunks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chunks (
    content text NOT NULL,
    embedding public.vector(1024),
    "position" integer DEFAULT 0 NOT NULL,
    start_line integer,
    end_line integer,
    page_start integer,
    page_end integer,
    document_id integer NOT NULL,
    workspace_id integer NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: chunks_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.chunks_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: chunks_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.chunks_id_seq OWNED BY public.chunks.id;


--
-- Name: connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.connections (
    provider character varying(100) NOT NULL,
    base_url character varying(500),
    api_key character varying,
    extra jsonb DEFAULT '{}'::jsonb NOT NULL,
    scope public.connectionscope NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    workspace_id integer,
    user_id uuid,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_connections_scope_owner CHECK ((((scope = 'GLOBAL'::public.connectionscope) AND (workspace_id IS NULL) AND (user_id IS NULL)) OR ((scope = 'SEARCH_SPACE'::public.connectionscope) AND (workspace_id IS NOT NULL) AND (user_id IS NOT NULL)) OR ((scope = 'USER'::public.connectionscope) AND (user_id IS NOT NULL))))
);


--
-- Name: connections_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.connections_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: connections_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.connections_id_seq OWNED BY public.connections.id;


--
-- Name: deliverable_jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deliverable_jobs (
    kind character varying(64) NOT NULL,
    title character varying(500) NOT NULL,
    workspace_id integer NOT NULL,
    thread_id integer,
    created_by_id uuid,
    tool_call_id text NOT NULL,
    request jsonb DEFAULT '{}'::jsonb NOT NULL,
    checkpoint jsonb DEFAULT '{}'::jsonb NOT NULL,
    status public.deliverable_job_status DEFAULT 'queued'::public.deliverable_job_status NOT NULL,
    phase character varying(64),
    progress integer DEFAULT 0 NOT NULL,
    artifact_id integer,
    celery_task_id character varying(255),
    attempt_count integer DEFAULT 1 NOT NULL,
    failure_code character varying(64),
    internal_error character varying(2000),
    cancel_requested_at timestamp with time zone,
    claimed_at timestamp with time zone,
    heartbeat_at timestamp with time zone,
    finished_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_deliverable_jobs_attempt_count CHECK ((attempt_count >= 1)),
    CONSTRAINT ck_deliverable_jobs_progress CHECK (((progress >= 0) AND (progress <= 100)))
);


--
-- Name: deliverable_jobs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.deliverable_jobs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: deliverable_jobs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.deliverable_jobs_id_seq OWNED BY public.deliverable_jobs.id;


--
-- Name: document_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.document_files (
    document_id integer NOT NULL,
    workspace_id integer NOT NULL,
    kind public.document_file_kind DEFAULT 'ORIGINAL'::public.document_file_kind NOT NULL,
    storage_backend character varying(32) NOT NULL,
    storage_key character varying NOT NULL,
    original_filename character varying NOT NULL,
    mime_type character varying,
    size_bytes bigint NOT NULL,
    checksum_sha256 character varying(64),
    created_by_id uuid,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: document_files_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.document_files_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: document_files_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.document_files_id_seq OWNED BY public.document_files.id;


--
-- Name: document_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.document_revisions (
    document_id integer,
    workspace_id integer NOT NULL,
    content_before text,
    title_before character varying,
    folder_id_before integer,
    chunks_before jsonb,
    metadata_before jsonb,
    created_by_turn_id text,
    agent_action_id integer,
    created_at timestamp with time zone DEFAULT (now() AT TIME ZONE 'utc'::text) NOT NULL,
    id integer NOT NULL
);


--
-- Name: document_revisions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.document_revisions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: document_revisions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.document_revisions_id_seq OWNED BY public.document_revisions.id;


--
-- Name: document_source_guides; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.document_source_guides (
    document_id integer NOT NULL,
    content_hash character varying(128) NOT NULL,
    summary text NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: document_source_guides_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.document_source_guides_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: document_source_guides_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.document_source_guides_id_seq OWNED BY public.document_source_guides.id;


--
-- Name: document_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.document_versions (
    document_id integer NOT NULL,
    version_number integer NOT NULL,
    source_markdown text,
    content_hash character varying NOT NULL,
    title character varying,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: document_versions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.document_versions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: document_versions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.document_versions_id_seq OWNED BY public.document_versions.id;


--
-- Name: documents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.documents (
    title character varying NOT NULL,
    document_type public.documenttype NOT NULL,
    document_metadata json,
    content text NOT NULL,
    content_hash character varying NOT NULL,
    unique_identifier_hash character varying,
    path character varying,
    embedding public.vector(1024),
    blocknote_document jsonb,
    source_markdown text,
    content_needs_reindexing boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone,
    workspace_id integer NOT NULL,
    folder_id integer,
    created_by_id uuid,
    connector_id integer,
    status jsonb DEFAULT '{"state": "ready"}'::jsonb NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: documents_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.documents_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: documents_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.documents_id_seq OWNED BY public.documents.id;


--
-- Name: embedding_cache_sets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.embedding_cache_sets (
    markdown_sha256 character varying(64) NOT NULL,
    embedding_model character varying(255) NOT NULL,
    embedding_dim integer NOT NULL,
    chunker_kind character varying(8) NOT NULL,
    chunker_version integer NOT NULL,
    storage_backend character varying(32) NOT NULL,
    storage_key character varying NOT NULL,
    size_bytes bigint NOT NULL,
    chunk_count integer DEFAULT 0 NOT NULL,
    times_reused bigint DEFAULT '0'::bigint NOT NULL,
    last_used_at timestamp with time zone NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: embedding_cache_sets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.embedding_cache_sets_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: embedding_cache_sets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.embedding_cache_sets_id_seq OWNED BY public.embedding_cache_sets.id;


--
-- Name: etl_cache_parses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.etl_cache_parses (
    source_sha256 character varying(64) NOT NULL,
    etl_service character varying(32) NOT NULL,
    mode character varying(16) NOT NULL,
    parser_version integer NOT NULL,
    storage_backend character varying(32) NOT NULL,
    storage_key character varying NOT NULL,
    size_bytes bigint NOT NULL,
    content_type character varying(32) NOT NULL,
    actual_pages integer DEFAULT 0 NOT NULL,
    times_reused bigint DEFAULT '0'::bigint NOT NULL,
    last_used_at timestamp with time zone NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: etl_cache_parses_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.etl_cache_parses_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: etl_cache_parses_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.etl_cache_parses_id_seq OWNED BY public.etl_cache_parses.id;


--
-- Name: folder_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.folder_revisions (
    folder_id integer,
    workspace_id integer NOT NULL,
    name_before character varying(255),
    parent_id_before integer,
    position_before character varying(50),
    created_by_turn_id text,
    agent_action_id integer,
    created_at timestamp with time zone DEFAULT (now() AT TIME ZONE 'utc'::text) NOT NULL,
    id integer NOT NULL
);


--
-- Name: folder_revisions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.folder_revisions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: folder_revisions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.folder_revisions_id_seq OWNED BY public.folder_revisions.id;


--
-- Name: folders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.folders (
    name character varying(255) NOT NULL,
    "position" character varying(50) NOT NULL,
    parent_id integer,
    workspace_id integer NOT NULL,
    created_by_id uuid,
    updated_at timestamp with time zone NOT NULL,
    metadata jsonb,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: folders_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.folders_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: folders_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.folders_id_seq OWNED BY public.folders.id;


--
-- Name: logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logs (
    level public.loglevel NOT NULL,
    status public.logstatus NOT NULL,
    message text NOT NULL,
    source character varying(200),
    log_metadata json,
    workspace_id integer NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: logs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.logs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: logs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.logs_id_seq OWNED BY public.logs.id;


--
-- Name: model_compatibility; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.model_compatibility (
    id integer NOT NULL,
    model_id character varying(255) NOT NULL,
    status character varying(16) NOT NULL,
    failure_stage character varying(32),
    error_code character varying(64),
    error_excerpt text,
    latency_ms integer,
    checked_at timestamp with time zone NOT NULL
);


--
-- Name: model_compatibility_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.model_compatibility_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: model_compatibility_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.model_compatibility_id_seq OWNED BY public.model_compatibility.id;


--
-- Name: models; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.models (
    connection_id integer NOT NULL,
    model_id character varying(255) NOT NULL,
    display_name character varying(255),
    source public.modelsource DEFAULT 'DISCOVERED'::public.modelsource NOT NULL,
    supports_chat boolean,
    max_input_tokens integer,
    supports_image_input boolean,
    supports_tools boolean,
    supports_image_generation boolean,
    capabilities_override jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    billing_tier character varying(50),
    catalog jsonb DEFAULT '{}'::jsonb NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: models_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.models_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: models_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.models_id_seq OWNED BY public.models.id;


--
-- Name: new_chat_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.new_chat_messages (
    role public.newchatmessagerole NOT NULL,
    content jsonb NOT NULL,
    thread_id integer NOT NULL,
    author_id uuid,
    turn_id character varying(64),
    source text DEFAULT 'surfsense'::text NOT NULL,
    platform_metadata jsonb,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: new_chat_messages_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.new_chat_messages_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: new_chat_messages_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.new_chat_messages_id_seq OWNED BY public.new_chat_messages.id;


--
-- Name: new_chat_threads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.new_chat_threads (
    title character varying(500) NOT NULL,
    archived boolean NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    visibility public.chatvisibility DEFAULT 'PRIVATE'::public.chatvisibility NOT NULL,
    workspace_id integer NOT NULL,
    created_by_id uuid,
    cloned_from_thread_id integer,
    cloned_from_snapshot_id integer,
    cloned_at timestamp with time zone,
    needs_history_bootstrap boolean DEFAULT false NOT NULL,
    pinned_llm_config_id integer,
    source text DEFAULT 'surfsense'::text NOT NULL,
    notebook_id integer,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: new_chat_threads_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.new_chat_threads_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: new_chat_threads_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.new_chat_threads_id_seq OWNED BY public.new_chat_threads.id;


--
-- Name: notebook_earlier_chats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_earlier_chats (
    thread_id integer NOT NULL,
    notebook_id integer NOT NULL,
    workspace_id integer NOT NULL,
    moved_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: notebook_features; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_features (
    notebook_id integer NOT NULL,
    user_id uuid NOT NULL,
    featured_at timestamp with time zone DEFAULT now() NOT NULL,
    id integer NOT NULL
);


--
-- Name: notebook_features_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebook_features_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebook_features_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebook_features_id_seq OWNED BY public.notebook_features.id;


--
-- Name: notebook_notes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_notes (
    notebook_id integer NOT NULL,
    title character varying(300) NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    converted_document_id integer,
    origin character varying(16) DEFAULT 'hand'::character varying NOT NULL,
    automation_id integer,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_notebook_notes_origin CHECK (((origin)::text = ANY ((ARRAY['hand'::character varying, 'chat'::character varying, 'automation'::character varying])::text[])))
);


--
-- Name: notebook_notes_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebook_notes_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebook_notes_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebook_notes_id_seq OWNED BY public.notebook_notes.id;


--
-- Name: notebook_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_outputs (
    notebook_id integer NOT NULL,
    workspace_id integer NOT NULL,
    kind character varying(32) NOT NULL,
    title character varying(500) NOT NULL,
    options jsonb DEFAULT '{}'::jsonb NOT NULL,
    source_document_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    podcast_id integer,
    deliverable_job_id integer,
    thread_id integer,
    created_by_id uuid,
    generated_by jsonb,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_notebook_outputs_one_run CHECK ((num_nonnulls(podcast_id, deliverable_job_id) <= 1))
);


--
-- Name: notebook_outputs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebook_outputs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebook_outputs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebook_outputs_id_seq OWNED BY public.notebook_outputs.id;


--
-- Name: notebook_public_links; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_public_links (
    notebook_id integer NOT NULL,
    workspace_id integer NOT NULL,
    share_token character varying(128) NOT NULL,
    created_by_user_id uuid,
    expires_at timestamp with time zone,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: notebook_public_links_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebook_public_links_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebook_public_links_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebook_public_links_id_seq OWNED BY public.notebook_public_links.id;


--
-- Name: notebook_report_templates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_report_templates (
    workspace_id integer NOT NULL,
    template_key character varying(64) NOT NULL,
    title character varying(200) NOT NULL,
    description character varying(500) DEFAULT ''::character varying NOT NULL,
    prompt text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: notebook_report_templates_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebook_report_templates_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebook_report_templates_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebook_report_templates_id_seq OWNED BY public.notebook_report_templates.id;


--
-- Name: notebook_sources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebook_sources (
    notebook_id integer NOT NULL,
    document_id integer,
    folder_id integer,
    enabled boolean DEFAULT true NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    id integer NOT NULL,
    CONSTRAINT ck_notebook_sources_one_target CHECK ((num_nonnulls(document_id, folder_id) = 1))
);


--
-- Name: notebook_sources_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebook_sources_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebook_sources_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebook_sources_id_seq OWNED BY public.notebook_sources.id;


--
-- Name: notebooks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notebooks (
    workspace_id integer NOT NULL,
    title character varying(200) NOT NULL,
    icon character varying(16),
    created_by_id uuid,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    focus_suggestions jsonb,
    focus_suggestions_key character varying(64),
    chat_goal character varying(16) DEFAULT 'default'::character varying NOT NULL,
    chat_instructions text,
    response_length character varying(16) DEFAULT 'default'::character varying NOT NULL,
    title_source character varying(8) DEFAULT 'user'::character varying NOT NULL,
    icon_source character varying(8) DEFAULT 'user'::character varying NOT NULL,
    auto_title jsonb,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_notebooks_chat_goal CHECK (((chat_goal)::text = ANY ((ARRAY['default'::character varying, 'learning_guide'::character varying, 'custom'::character varying])::text[]))),
    CONSTRAINT ck_notebooks_icon_source CHECK (((icon_source)::text = ANY ((ARRAY['auto'::character varying, 'user'::character varying])::text[]))),
    CONSTRAINT ck_notebooks_response_length CHECK (((response_length)::text = ANY ((ARRAY['default'::character varying, 'longer'::character varying, 'shorter'::character varying])::text[]))),
    CONSTRAINT ck_notebooks_title_source CHECK (((title_source)::text = ANY ((ARRAY['auto'::character varying, 'user'::character varying])::text[])))
);


--
-- Name: notebooks_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notebooks_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notebooks_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notebooks_id_seq OWNED BY public.notebooks.id;


--
-- Name: notifications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notifications (
    user_id uuid NOT NULL,
    workspace_id integer,
    type character varying(50) NOT NULL,
    title character varying(200) NOT NULL,
    message text NOT NULL,
    read boolean DEFAULT false NOT NULL,
    metadata jsonb,
    updated_at timestamp with time zone,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: notifications_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notifications_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notifications_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notifications_id_seq OWNED BY public.notifications.id;


--
-- Name: oauth_account; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_account (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    oauth_name character varying(100) NOT NULL,
    access_token character varying(1024) NOT NULL,
    expires_at integer,
    refresh_token character varying(1024),
    account_id character varying(320) NOT NULL,
    account_email character varying(320) NOT NULL
);


--
-- Name: personal_access_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_access_tokens (
    user_id uuid NOT NULL,
    token_hash character varying(64) NOT NULL,
    token_prefix character varying(16) NOT NULL,
    label character varying NOT NULL,
    expires_at timestamp with time zone,
    last_used_at timestamp with time zone,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: personal_access_tokens_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.personal_access_tokens_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: personal_access_tokens_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.personal_access_tokens_id_seq OWNED BY public.personal_access_tokens.id;


--
-- Name: podcast_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.podcast_runs (
    title character varying(500) NOT NULL,
    status public.podcast_status DEFAULT 'pending'::public.podcast_status NOT NULL,
    source_content text,
    spec jsonb,
    spec_version integer DEFAULT 1 NOT NULL,
    podcast_transcript jsonb,
    duration_seconds integer,
    error text,
    artifact_id integer,
    workspace_id integer NOT NULL,
    thread_id integer,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: podcast_runs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.podcast_runs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: podcast_runs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.podcast_runs_id_seq OWNED BY public.podcast_runs.id;


--
-- Name: prompts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.prompts (
    user_id uuid NOT NULL,
    workspace_id integer,
    default_prompt_slug character varying(100),
    name character varying(200) NOT NULL,
    prompt text NOT NULL,
    mode public.prompt_mode NOT NULL,
    version integer NOT NULL,
    is_public boolean NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: prompts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.prompts_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: prompts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.prompts_id_seq OWNED BY public.prompts.id;


--
-- Name: public_chat_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.public_chat_snapshots (
    thread_id integer,
    notebook_output_id integer,
    workspace_id integer NOT NULL,
    expires_at timestamp with time zone,
    share_token character varying(64) NOT NULL,
    content_hash character varying(64) NOT NULL,
    snapshot_data jsonb NOT NULL,
    message_ids integer[] NOT NULL,
    created_by_user_id uuid,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_public_snapshot_one_subject CHECK ((num_nonnulls(thread_id, notebook_output_id) = 1))
);


--
-- Name: public_chat_snapshots_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.public_chat_snapshots_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: public_chat_snapshots_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.public_chat_snapshots_id_seq OWNED BY public.public_chat_snapshots.id;


--
-- Name: push_devices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.push_devices (
    user_id uuid NOT NULL,
    token character varying(255) NOT NULL,
    platform character varying(16) NOT NULL,
    label character varying(120),
    language character varying(16),
    session_family_id uuid,
    last_seen_at timestamp with time zone NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: push_devices_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.push_devices_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: push_devices_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.push_devices_id_seq OWNED BY public.push_devices.id;


--
-- Name: push_preferences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.push_preferences (
    user_id uuid NOT NULL,
    choices jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: push_tickets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.push_tickets (
    ticket_id character varying(64) NOT NULL,
    token character varying(255) NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: push_tickets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.push_tickets_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: push_tickets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.push_tickets_id_seq OWNED BY public.push_tickets.id;


--
-- Name: refresh_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.refresh_tokens (
    id integer NOT NULL,
    user_id uuid NOT NULL,
    token_hash character varying(64) NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    absolute_expiry timestamp with time zone,
    family_id uuid NOT NULL,
    kind character varying(16) DEFAULT 'web'::character varying NOT NULL,
    device_label character varying(100),
    signed_in_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_refresh_tokens_kind CHECK (((kind)::text = ANY ((ARRAY['web'::character varying, 'app'::character varying])::text[])))
);


--
-- Name: refresh_tokens_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.refresh_tokens_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: refresh_tokens_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.refresh_tokens_id_seq OWNED BY public.refresh_tokens.id;


--
-- Name: runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.runs (
    id uuid NOT NULL,
    workspace_id integer NOT NULL,
    user_id uuid,
    thread_id character varying(255),
    capability character varying(100) NOT NULL,
    origin character varying(16) NOT NULL,
    status character varying(16) NOT NULL,
    error text,
    input jsonb,
    output_text text,
    item_count integer NOT NULL,
    char_count integer NOT NULL,
    duration_ms integer,
    cost_micros bigint,
    progress jsonb,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: search_source_connector_enablements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.search_source_connector_enablements (
    connector_id integer NOT NULL,
    workspace_id integer NOT NULL,
    enabled_by_user_id uuid,
    enabled boolean DEFAULT true NOT NULL,
    is_indexable boolean NOT NULL,
    last_indexed_at timestamp with time zone,
    periodic_indexing_enabled boolean NOT NULL,
    indexing_frequency_minutes integer,
    next_scheduled_at timestamp with time zone,
    indexing_config json,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: search_source_connector_enablements_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.search_source_connector_enablements_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: search_source_connector_enablements_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.search_source_connector_enablements_id_seq OWNED BY public.search_source_connector_enablements.id;


--
-- Name: search_source_connectors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.search_source_connectors (
    name character varying(100) NOT NULL,
    connector_type public.searchsourceconnectortype NOT NULL,
    is_indexable boolean NOT NULL,
    last_indexed_at timestamp with time zone,
    config json NOT NULL,
    enable_vision_llm boolean DEFAULT false NOT NULL,
    periodic_indexing_enabled boolean NOT NULL,
    indexing_frequency_minutes integer,
    next_scheduled_at timestamp with time zone,
    workspace_id integer,
    user_id uuid NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: search_source_connectors_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.search_source_connectors_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: search_source_connectors_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.search_source_connectors_id_seq OWNED BY public.search_source_connectors.id;


--
-- Name: signup_credit_claims; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.signup_credit_claims (
    identity_kind character varying NOT NULL,
    identity_fingerprint character varying(64) NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: signup_credit_claims_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.signup_credit_claims_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: signup_credit_claims_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.signup_credit_claims_id_seq OWNED BY public.signup_credit_claims.id;


--
-- Name: site_announcements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_announcements (
    title character varying(200) NOT NULL,
    body text NOT NULL,
    link character varying(2048),
    audience character varying(20) NOT NULL,
    workspace_id integer,
    created_by_id uuid,
    expires_at timestamp with time zone,
    withdrawn_at timestamp with time zone,
    recipient_count integer DEFAULT 0 NOT NULL,
    cleared_read_count integer DEFAULT 0 NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: site_announcements_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.site_announcements_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: site_announcements_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.site_announcements_id_seq OWNED BY public.site_announcements.id;


--
-- Name: token_usage; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.token_usage (
    prompt_tokens integer NOT NULL,
    completion_tokens integer NOT NULL,
    total_tokens integer NOT NULL,
    cost_micros bigint DEFAULT '0'::bigint NOT NULL,
    model_breakdown jsonb,
    call_details jsonb,
    usage_type character varying(50) NOT NULL,
    thread_id integer,
    message_id integer,
    workspace_id integer NOT NULL,
    user_id uuid NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: token_usage_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.token_usage_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: token_usage_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.token_usage_id_seq OWNED BY public.token_usage.id;


--
-- Name: tool_output_spills; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool_output_spills (
    id uuid NOT NULL,
    workspace_id integer,
    thread_id character varying(255),
    tool_name character varying(255),
    content text NOT NULL,
    char_count integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: user; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public."user" (
    credit_micros_balance bigint DEFAULT '0'::bigint NOT NULL,
    credit_micros_reserved bigint DEFAULT '0'::bigint NOT NULL,
    stripe_customer_id character varying,
    display_name character varying,
    avatar_url character varying,
    last_login timestamp with time zone,
    memory_md text DEFAULT ''::text,
    id uuid NOT NULL,
    email character varying(320) NOT NULL,
    hashed_password character varying(1024) NOT NULL,
    is_active boolean NOT NULL,
    is_superuser boolean NOT NULL,
    is_verified boolean NOT NULL
);


--
-- Name: video_presentation_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.video_presentation_runs (
    title character varying(500) NOT NULL,
    status public.video_presentation_status DEFAULT 'ready'::public.video_presentation_status NOT NULL,
    error text,
    workspace_id integer NOT NULL,
    thread_id integer,
    artifact_id integer,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: video_presentation_runs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.video_presentation_runs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: video_presentation_runs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.video_presentation_runs_id_seq OWNED BY public.video_presentation_runs.id;


--
-- Name: workspace_git_remotes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_git_remotes (
    workspace_id integer NOT NULL,
    provider character varying(16) NOT NULL,
    url character varying(512) NOT NULL,
    branch character varying(255) NOT NULL,
    installation_id character varying(64),
    token text,
    last_pushed_revision character varying(64),
    last_pushed_at timestamp with time zone,
    last_push_error text,
    sourcepath character varying(255) NOT NULL,
    last_remote_sha character varying(64),
    last_local_revision character varying(64),
    last_synced_at timestamp with time zone,
    last_error_code character varying(32),
    last_conflict_paths text,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: workspace_git_remotes_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.workspace_git_remotes_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: workspace_git_remotes_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.workspace_git_remotes_id_seq OWNED BY public.workspace_git_remotes.id;


--
-- Name: workspace_invites; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_invites (
    invite_code character varying(64) NOT NULL,
    email character varying(320),
    last_sent_at timestamp with time zone,
    workspace_id integer NOT NULL,
    role_id integer,
    created_by_id uuid,
    expires_at timestamp with time zone,
    max_uses integer,
    uses_count integer NOT NULL,
    is_active boolean NOT NULL,
    name character varying(100),
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: workspace_invites_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.workspace_invites_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: workspace_invites_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.workspace_invites_id_seq OWNED BY public.workspace_invites.id;


--
-- Name: workspace_memberships; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_memberships (
    user_id uuid NOT NULL,
    workspace_id integer NOT NULL,
    role_id integer,
    is_owner boolean NOT NULL,
    joined_at timestamp with time zone NOT NULL,
    invited_by_invite_id integer,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: workspace_memberships_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.workspace_memberships_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: workspace_memberships_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.workspace_memberships_id_seq OWNED BY public.workspace_memberships.id;


--
-- Name: workspace_quick_models; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_quick_models (
    workspace_id integer NOT NULL,
    model_id integer NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: workspace_roles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_roles (
    name character varying(100) NOT NULL,
    description character varying(500),
    permissions character varying[] NOT NULL,
    is_default boolean NOT NULL,
    is_system_role boolean NOT NULL,
    workspace_id integer NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: workspace_roles_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.workspace_roles_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: workspace_roles_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.workspace_roles_id_seq OWNED BY public.workspace_roles.id;


--
-- Name: workspace_studio_models; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_studio_models (
    workspace_id integer NOT NULL,
    default_choice jsonb NOT NULL,
    overrides jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: workspaces; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspaces (
    name character varying(100) NOT NULL,
    description character varying(500),
    citations_enabled boolean NOT NULL,
    api_access_enabled boolean DEFAULT false NOT NULL,
    qna_custom_instructions text,
    shared_memory_md text DEFAULT ''::text,
    chat_model_id integer DEFAULT 0,
    image_gen_model_id integer DEFAULT 0,
    vision_model_id integer DEFAULT 0,
    llm_setup_completed_at timestamp with time zone,
    knowledge_store_enabled boolean DEFAULT false NOT NULL,
    last_indexed_revision character varying(64),
    user_id uuid NOT NULL,
    id integer NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: workspaces_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.workspaces_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: workspaces_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.workspaces_id_seq OWNED BY public.workspaces.id;


--
-- Name: agent_action_log id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_action_log ALTER COLUMN id SET DEFAULT nextval('public.agent_action_log_id_seq'::regclass);


--
-- Name: agent_permission_rules id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_permission_rules ALTER COLUMN id SET DEFAULT nextval('public.agent_permission_rules_id_seq'::regclass);


--
-- Name: artifact_files id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_files ALTER COLUMN id SET DEFAULT nextval('public.artifact_files_id_seq'::regclass);


--
-- Name: artifacts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts ALTER COLUMN id SET DEFAULT nextval('public.artifacts_id_seq'::regclass);


--
-- Name: automation_runs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_runs ALTER COLUMN id SET DEFAULT nextval('public.automation_runs_id_seq'::regclass);


--
-- Name: automation_triggers id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_triggers ALTER COLUMN id SET DEFAULT nextval('public.automation_triggers_id_seq'::regclass);


--
-- Name: automations id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automations ALTER COLUMN id SET DEFAULT nextval('public.automations_id_seq'::regclass);


--
-- Name: billing_ledger id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_ledger ALTER COLUMN id SET DEFAULT nextval('public.billing_ledger_id_seq'::regclass);


--
-- Name: billing_packs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_packs ALTER COLUMN id SET DEFAULT nextval('public.billing_packs_id_seq'::regclass);


--
-- Name: billing_payments id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_payments ALTER COLUMN id SET DEFAULT nextval('public.billing_payments_id_seq'::regclass);


--
-- Name: billing_plans id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_plans ALTER COLUMN id SET DEFAULT nextval('public.billing_plans_id_seq'::regclass);


--
-- Name: billing_usage id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_usage ALTER COLUMN id SET DEFAULT nextval('public.billing_usage_id_seq'::regclass);


--
-- Name: chat_comment_mentions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comment_mentions ALTER COLUMN id SET DEFAULT nextval('public.chat_comment_mentions_id_seq'::regclass);


--
-- Name: chat_comments id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comments ALTER COLUMN id SET DEFAULT nextval('public.chat_comments_id_seq'::regclass);


--
-- Name: chat_session_state id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_session_state ALTER COLUMN id SET DEFAULT nextval('public.chat_session_state_id_seq'::regclass);


--
-- Name: chunks id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks ALTER COLUMN id SET DEFAULT nextval('public.chunks_id_seq'::regclass);


--
-- Name: connections id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connections ALTER COLUMN id SET DEFAULT nextval('public.connections_id_seq'::regclass);


--
-- Name: deliverable_jobs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deliverable_jobs ALTER COLUMN id SET DEFAULT nextval('public.deliverable_jobs_id_seq'::regclass);


--
-- Name: document_files id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_files ALTER COLUMN id SET DEFAULT nextval('public.document_files_id_seq'::regclass);


--
-- Name: document_revisions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_revisions ALTER COLUMN id SET DEFAULT nextval('public.document_revisions_id_seq'::regclass);


--
-- Name: document_source_guides id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_source_guides ALTER COLUMN id SET DEFAULT nextval('public.document_source_guides_id_seq'::regclass);


--
-- Name: document_versions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_versions ALTER COLUMN id SET DEFAULT nextval('public.document_versions_id_seq'::regclass);


--
-- Name: documents id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents ALTER COLUMN id SET DEFAULT nextval('public.documents_id_seq'::regclass);


--
-- Name: embedding_cache_sets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.embedding_cache_sets ALTER COLUMN id SET DEFAULT nextval('public.embedding_cache_sets_id_seq'::regclass);


--
-- Name: etl_cache_parses id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.etl_cache_parses ALTER COLUMN id SET DEFAULT nextval('public.etl_cache_parses_id_seq'::regclass);


--
-- Name: folder_revisions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folder_revisions ALTER COLUMN id SET DEFAULT nextval('public.folder_revisions_id_seq'::regclass);


--
-- Name: folders id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders ALTER COLUMN id SET DEFAULT nextval('public.folders_id_seq'::regclass);


--
-- Name: logs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logs ALTER COLUMN id SET DEFAULT nextval('public.logs_id_seq'::regclass);


--
-- Name: model_compatibility id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.model_compatibility ALTER COLUMN id SET DEFAULT nextval('public.model_compatibility_id_seq'::regclass);


--
-- Name: models id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.models ALTER COLUMN id SET DEFAULT nextval('public.models_id_seq'::regclass);


--
-- Name: new_chat_messages id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_messages ALTER COLUMN id SET DEFAULT nextval('public.new_chat_messages_id_seq'::regclass);


--
-- Name: new_chat_threads id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads ALTER COLUMN id SET DEFAULT nextval('public.new_chat_threads_id_seq'::regclass);


--
-- Name: notebook_features id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_features ALTER COLUMN id SET DEFAULT nextval('public.notebook_features_id_seq'::regclass);


--
-- Name: notebook_notes id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_notes ALTER COLUMN id SET DEFAULT nextval('public.notebook_notes_id_seq'::regclass);


--
-- Name: notebook_outputs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs ALTER COLUMN id SET DEFAULT nextval('public.notebook_outputs_id_seq'::regclass);


--
-- Name: notebook_public_links id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_public_links ALTER COLUMN id SET DEFAULT nextval('public.notebook_public_links_id_seq'::regclass);


--
-- Name: notebook_report_templates id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_report_templates ALTER COLUMN id SET DEFAULT nextval('public.notebook_report_templates_id_seq'::regclass);


--
-- Name: notebook_sources id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources ALTER COLUMN id SET DEFAULT nextval('public.notebook_sources_id_seq'::regclass);


--
-- Name: notebooks id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebooks ALTER COLUMN id SET DEFAULT nextval('public.notebooks_id_seq'::regclass);


--
-- Name: notifications id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notifications ALTER COLUMN id SET DEFAULT nextval('public.notifications_id_seq'::regclass);


--
-- Name: personal_access_tokens id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_access_tokens ALTER COLUMN id SET DEFAULT nextval('public.personal_access_tokens_id_seq'::regclass);


--
-- Name: podcast_runs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.podcast_runs ALTER COLUMN id SET DEFAULT nextval('public.podcast_runs_id_seq'::regclass);


--
-- Name: prompts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompts ALTER COLUMN id SET DEFAULT nextval('public.prompts_id_seq'::regclass);


--
-- Name: public_chat_snapshots id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots ALTER COLUMN id SET DEFAULT nextval('public.public_chat_snapshots_id_seq'::regclass);


--
-- Name: push_devices id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_devices ALTER COLUMN id SET DEFAULT nextval('public.push_devices_id_seq'::regclass);


--
-- Name: push_tickets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_tickets ALTER COLUMN id SET DEFAULT nextval('public.push_tickets_id_seq'::regclass);


--
-- Name: refresh_tokens id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens ALTER COLUMN id SET DEFAULT nextval('public.refresh_tokens_id_seq'::regclass);


--
-- Name: search_source_connector_enablements id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connector_enablements ALTER COLUMN id SET DEFAULT nextval('public.search_source_connector_enablements_id_seq'::regclass);


--
-- Name: search_source_connectors id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connectors ALTER COLUMN id SET DEFAULT nextval('public.search_source_connectors_id_seq'::regclass);


--
-- Name: signup_credit_claims id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.signup_credit_claims ALTER COLUMN id SET DEFAULT nextval('public.signup_credit_claims_id_seq'::regclass);


--
-- Name: site_announcements id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_announcements ALTER COLUMN id SET DEFAULT nextval('public.site_announcements_id_seq'::regclass);


--
-- Name: token_usage id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_usage ALTER COLUMN id SET DEFAULT nextval('public.token_usage_id_seq'::regclass);


--
-- Name: video_presentation_runs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_presentation_runs ALTER COLUMN id SET DEFAULT nextval('public.video_presentation_runs_id_seq'::regclass);


--
-- Name: workspace_git_remotes id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_git_remotes ALTER COLUMN id SET DEFAULT nextval('public.workspace_git_remotes_id_seq'::regclass);


--
-- Name: workspace_invites id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_invites ALTER COLUMN id SET DEFAULT nextval('public.workspace_invites_id_seq'::regclass);


--
-- Name: workspace_memberships id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships ALTER COLUMN id SET DEFAULT nextval('public.workspace_memberships_id_seq'::regclass);


--
-- Name: workspace_roles id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_roles ALTER COLUMN id SET DEFAULT nextval('public.workspace_roles_id_seq'::regclass);


--
-- Name: workspaces id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspaces ALTER COLUMN id SET DEFAULT nextval('public.workspaces_id_seq'::regclass);


--
-- Name: agent_action_log agent_action_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_action_log
    ADD CONSTRAINT agent_action_log_pkey PRIMARY KEY (id);


--
-- Name: agent_permission_rules agent_permission_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_permission_rules
    ADD CONSTRAINT agent_permission_rules_pkey PRIMARY KEY (id);


--
-- Name: alembic_version alembic_version_pkc; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alembic_version
    ADD CONSTRAINT alembic_version_pkc PRIMARY KEY (version_num);


--
-- Name: artifact_files artifact_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_files
    ADD CONSTRAINT artifact_files_pkey PRIMARY KEY (id);


--
-- Name: artifacts artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_pkey PRIMARY KEY (id);


--
-- Name: automation_runs automation_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_runs
    ADD CONSTRAINT automation_runs_pkey PRIMARY KEY (id);


--
-- Name: automation_triggers automation_triggers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_triggers
    ADD CONSTRAINT automation_triggers_pkey PRIMARY KEY (id);


--
-- Name: automations automations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automations
    ADD CONSTRAINT automations_pkey PRIMARY KEY (id);


--
-- Name: billing_accounts billing_accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_accounts
    ADD CONSTRAINT billing_accounts_pkey PRIMARY KEY (user_id);


--
-- Name: billing_action_prices billing_action_prices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_action_prices
    ADD CONSTRAINT billing_action_prices_pkey PRIMARY KEY (action);


--
-- Name: billing_ledger billing_ledger_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_ledger
    ADD CONSTRAINT billing_ledger_pkey PRIMARY KEY (id);


--
-- Name: billing_packs billing_packs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_packs
    ADD CONSTRAINT billing_packs_pkey PRIMARY KEY (id);


--
-- Name: billing_packs billing_packs_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_packs
    ADD CONSTRAINT billing_packs_slug_key UNIQUE (slug);


--
-- Name: billing_payments billing_payments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_payments
    ADD CONSTRAINT billing_payments_pkey PRIMARY KEY (id);


--
-- Name: billing_plans billing_plans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_plans
    ADD CONSTRAINT billing_plans_pkey PRIMARY KEY (id);


--
-- Name: billing_plans billing_plans_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_plans
    ADD CONSTRAINT billing_plans_slug_key UNIQUE (slug);


--
-- Name: billing_settings billing_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_settings
    ADD CONSTRAINT billing_settings_pkey PRIMARY KEY (key);


--
-- Name: billing_usage billing_usage_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_usage
    ADD CONSTRAINT billing_usage_pkey PRIMARY KEY (id);


--
-- Name: billing_webhook_events billing_webhook_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_webhook_events
    ADD CONSTRAINT billing_webhook_events_pkey PRIMARY KEY (provider, event_id);


--
-- Name: chat_comment_mentions chat_comment_mentions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comment_mentions
    ADD CONSTRAINT chat_comment_mentions_pkey PRIMARY KEY (id);


--
-- Name: chat_comments chat_comments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comments
    ADD CONSTRAINT chat_comments_pkey PRIMARY KEY (id);


--
-- Name: chat_session_state chat_session_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_session_state
    ADD CONSTRAINT chat_session_state_pkey PRIMARY KEY (id);


--
-- Name: chunks chunks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_pkey PRIMARY KEY (id);


--
-- Name: connections connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connections
    ADD CONSTRAINT connections_pkey PRIMARY KEY (id);


--
-- Name: deliverable_jobs deliverable_jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deliverable_jobs
    ADD CONSTRAINT deliverable_jobs_pkey PRIMARY KEY (id);


--
-- Name: document_files document_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_files
    ADD CONSTRAINT document_files_pkey PRIMARY KEY (id);


--
-- Name: document_revisions document_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_revisions
    ADD CONSTRAINT document_revisions_pkey PRIMARY KEY (id);


--
-- Name: document_source_guides document_source_guides_document_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_source_guides
    ADD CONSTRAINT document_source_guides_document_id_key UNIQUE (document_id);


--
-- Name: document_source_guides document_source_guides_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_source_guides
    ADD CONSTRAINT document_source_guides_pkey PRIMARY KEY (id);


--
-- Name: document_versions document_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_versions
    ADD CONSTRAINT document_versions_pkey PRIMARY KEY (id);


--
-- Name: documents documents_id_workspace_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_id_workspace_id_key UNIQUE (id, workspace_id);


--
-- Name: documents documents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_pkey PRIMARY KEY (id);


--
-- Name: embedding_cache_sets embedding_cache_sets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.embedding_cache_sets
    ADD CONSTRAINT embedding_cache_sets_pkey PRIMARY KEY (id);


--
-- Name: etl_cache_parses etl_cache_parses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.etl_cache_parses
    ADD CONSTRAINT etl_cache_parses_pkey PRIMARY KEY (id);


--
-- Name: folder_revisions folder_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folder_revisions
    ADD CONSTRAINT folder_revisions_pkey PRIMARY KEY (id);


--
-- Name: folders folders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders
    ADD CONSTRAINT folders_pkey PRIMARY KEY (id);


--
-- Name: logs logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logs
    ADD CONSTRAINT logs_pkey PRIMARY KEY (id);


--
-- Name: model_compatibility model_compatibility_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.model_compatibility
    ADD CONSTRAINT model_compatibility_pkey PRIMARY KEY (id);


--
-- Name: models models_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.models
    ADD CONSTRAINT models_pkey PRIMARY KEY (id);


--
-- Name: new_chat_messages new_chat_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_messages
    ADD CONSTRAINT new_chat_messages_pkey PRIMARY KEY (id);


--
-- Name: new_chat_threads new_chat_threads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads
    ADD CONSTRAINT new_chat_threads_pkey PRIMARY KEY (id);


--
-- Name: notebook_earlier_chats notebook_earlier_chats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_earlier_chats
    ADD CONSTRAINT notebook_earlier_chats_pkey PRIMARY KEY (thread_id);


--
-- Name: notebook_features notebook_features_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_features
    ADD CONSTRAINT notebook_features_pkey PRIMARY KEY (id);


--
-- Name: notebook_notes notebook_notes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_notes
    ADD CONSTRAINT notebook_notes_pkey PRIMARY KEY (id);


--
-- Name: notebook_outputs notebook_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_pkey PRIMARY KEY (id);


--
-- Name: notebook_public_links notebook_public_links_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_public_links
    ADD CONSTRAINT notebook_public_links_pkey PRIMARY KEY (id);


--
-- Name: notebook_report_templates notebook_report_templates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_report_templates
    ADD CONSTRAINT notebook_report_templates_pkey PRIMARY KEY (id);


--
-- Name: notebook_sources notebook_sources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources
    ADD CONSTRAINT notebook_sources_pkey PRIMARY KEY (id);


--
-- Name: notebooks notebooks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebooks
    ADD CONSTRAINT notebooks_pkey PRIMARY KEY (id);


--
-- Name: notifications notifications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_pkey PRIMARY KEY (id);


--
-- Name: oauth_account oauth_account_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_account
    ADD CONSTRAINT oauth_account_pkey PRIMARY KEY (id);


--
-- Name: personal_access_tokens personal_access_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_access_tokens
    ADD CONSTRAINT personal_access_tokens_pkey PRIMARY KEY (id);


--
-- Name: podcast_runs podcast_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.podcast_runs
    ADD CONSTRAINT podcast_runs_pkey PRIMARY KEY (id);


--
-- Name: prompts prompts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompts
    ADD CONSTRAINT prompts_pkey PRIMARY KEY (id);


--
-- Name: public_chat_snapshots public_chat_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots
    ADD CONSTRAINT public_chat_snapshots_pkey PRIMARY KEY (id);


--
-- Name: push_devices push_devices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_devices
    ADD CONSTRAINT push_devices_pkey PRIMARY KEY (id);


--
-- Name: push_preferences push_preferences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_preferences
    ADD CONSTRAINT push_preferences_pkey PRIMARY KEY (user_id);


--
-- Name: push_tickets push_tickets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_tickets
    ADD CONSTRAINT push_tickets_pkey PRIMARY KEY (id);


--
-- Name: refresh_tokens refresh_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_pkey PRIMARY KEY (id);


--
-- Name: runs runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runs
    ADD CONSTRAINT runs_pkey PRIMARY KEY (id);


--
-- Name: search_source_connector_enablements search_source_connector_enablements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connector_enablements
    ADD CONSTRAINT search_source_connector_enablements_pkey PRIMARY KEY (id);


--
-- Name: search_source_connectors search_source_connectors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connectors
    ADD CONSTRAINT search_source_connectors_pkey PRIMARY KEY (id);


--
-- Name: signup_credit_claims signup_credit_claims_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.signup_credit_claims
    ADD CONSTRAINT signup_credit_claims_pkey PRIMARY KEY (id);


--
-- Name: site_announcements site_announcements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_announcements
    ADD CONSTRAINT site_announcements_pkey PRIMARY KEY (id);


--
-- Name: token_usage token_usage_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_usage
    ADD CONSTRAINT token_usage_pkey PRIMARY KEY (id);


--
-- Name: tool_output_spills tool_output_spills_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_output_spills
    ADD CONSTRAINT tool_output_spills_pkey PRIMARY KEY (id);


--
-- Name: agent_permission_rules uq_agent_permission_rules_scope; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_permission_rules
    ADD CONSTRAINT uq_agent_permission_rules_scope UNIQUE (workspace_id, user_id, thread_id, permission, pattern, action);


--
-- Name: artifact_files uq_artifact_files_artifact_role; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_files
    ADD CONSTRAINT uq_artifact_files_artifact_role UNIQUE (artifact_id, role);


--
-- Name: artifact_files uq_artifact_files_storage_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_files
    ADD CONSTRAINT uq_artifact_files_storage_key UNIQUE (storage_key);


--
-- Name: artifacts uq_artifacts_document_id; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT uq_artifacts_document_id UNIQUE (document_id);


--
-- Name: billing_payments uq_billing_payments_ref; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_payments
    ADD CONSTRAINT uq_billing_payments_ref UNIQUE (provider, provider_ref);


--
-- Name: search_source_connector_enablements uq_connector_workspace_enablement; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connector_enablements
    ADD CONSTRAINT uq_connector_workspace_enablement UNIQUE (connector_id, workspace_id);


--
-- Name: document_versions uq_document_version; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_versions
    ADD CONSTRAINT uq_document_version UNIQUE (document_id, version_number);


--
-- Name: embedding_cache_sets uq_embedding_cache_sets_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.embedding_cache_sets
    ADD CONSTRAINT uq_embedding_cache_sets_key UNIQUE (markdown_sha256, embedding_model, chunker_kind, chunker_version);


--
-- Name: etl_cache_parses uq_etl_cache_parses_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.etl_cache_parses
    ADD CONSTRAINT uq_etl_cache_parses_key UNIQUE (source_sha256, etl_service, mode, parser_version);


--
-- Name: models uq_models_connection_model_id; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.models
    ADD CONSTRAINT uq_models_connection_model_id UNIQUE (connection_id, model_id);


--
-- Name: notebook_features uq_notebook_features_user; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_features
    ADD CONSTRAINT uq_notebook_features_user UNIQUE (notebook_id, user_id);


--
-- Name: notebook_public_links uq_notebook_public_links_notebook; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_public_links
    ADD CONSTRAINT uq_notebook_public_links_notebook UNIQUE (notebook_id);


--
-- Name: notebook_report_templates uq_notebook_report_templates_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_report_templates
    ADD CONSTRAINT uq_notebook_report_templates_key UNIQUE (workspace_id, template_key);


--
-- Name: notebook_sources uq_notebook_sources_document; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources
    ADD CONSTRAINT uq_notebook_sources_document UNIQUE (notebook_id, document_id);


--
-- Name: notebook_sources uq_notebook_sources_folder; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources
    ADD CONSTRAINT uq_notebook_sources_folder UNIQUE (notebook_id, folder_id);


--
-- Name: prompts uq_prompt_user_default_slug; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompts
    ADD CONSTRAINT uq_prompt_user_default_slug UNIQUE (user_id, default_prompt_slug);


--
-- Name: signup_credit_claims uq_signup_credit_claims_identity; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.signup_credit_claims
    ADD CONSTRAINT uq_signup_credit_claims_identity UNIQUE (identity_kind, identity_fingerprint);


--
-- Name: public_chat_snapshots uq_snapshot_thread_content_hash; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots
    ADD CONSTRAINT uq_snapshot_thread_content_hash UNIQUE (thread_id, content_hash);


--
-- Name: workspace_memberships uq_user_workspace_membership; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships
    ADD CONSTRAINT uq_user_workspace_membership UNIQUE (user_id, workspace_id);


--
-- Name: workspace_git_remotes uq_workspace_git_remotes_workspace; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_git_remotes
    ADD CONSTRAINT uq_workspace_git_remotes_workspace UNIQUE (workspace_id);


--
-- Name: workspace_roles uq_workspace_role_name; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_roles
    ADD CONSTRAINT uq_workspace_role_name UNIQUE (workspace_id, name);


--
-- Name: search_source_connectors uq_workspace_user_connector_type_name; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connectors
    ADD CONSTRAINT uq_workspace_user_connector_type_name UNIQUE (workspace_id, user_id, connector_type, name);


--
-- Name: user user_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public."user"
    ADD CONSTRAINT user_pkey PRIMARY KEY (id);


--
-- Name: video_presentation_runs video_presentation_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_presentation_runs
    ADD CONSTRAINT video_presentation_runs_pkey PRIMARY KEY (id);


--
-- Name: workspace_git_remotes workspace_git_remotes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_git_remotes
    ADD CONSTRAINT workspace_git_remotes_pkey PRIMARY KEY (id);


--
-- Name: workspace_invites workspace_invites_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_invites
    ADD CONSTRAINT workspace_invites_pkey PRIMARY KEY (id);


--
-- Name: workspace_memberships workspace_memberships_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships
    ADD CONSTRAINT workspace_memberships_pkey PRIMARY KEY (id);


--
-- Name: workspace_quick_models workspace_quick_models_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_quick_models
    ADD CONSTRAINT workspace_quick_models_pkey PRIMARY KEY (workspace_id);


--
-- Name: workspace_roles workspace_roles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_roles
    ADD CONSTRAINT workspace_roles_pkey PRIMARY KEY (id);


--
-- Name: workspace_studio_models workspace_studio_models_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_studio_models
    ADD CONSTRAINT workspace_studio_models_pkey PRIMARY KEY (workspace_id);


--
-- Name: workspaces workspaces_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspaces
    ADD CONSTRAINT workspaces_pkey PRIMARY KEY (id);


--
-- Name: chucks_search_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chucks_search_index ON public.chunks USING gin (to_tsvector('english'::regconfig, content));


--
-- Name: chucks_vector_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX chucks_vector_index ON public.chunks USING hnsw (embedding public.vector_cosine_ops);


--
-- Name: document_search_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX document_search_index ON public.documents USING gin (to_tsvector('english'::regconfig, content));


--
-- Name: document_vector_index; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX document_vector_index ON public.documents USING hnsw (embedding public.vector_cosine_ops);


--
-- Name: ix_agent_action_log_chat_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_chat_turn_id ON public.agent_action_log USING btree (chat_turn_id);


--
-- Name: ix_agent_action_log_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_created_at ON public.agent_action_log USING btree (created_at);


--
-- Name: ix_agent_action_log_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_id ON public.agent_action_log USING btree (id);


--
-- Name: ix_agent_action_log_message_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_message_id ON public.agent_action_log USING hash (message_id);


--
-- Name: ix_agent_action_log_reverse_of; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_reverse_of ON public.agent_action_log USING btree (reverse_of);


--
-- Name: ix_agent_action_log_thread_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_thread_created ON public.agent_action_log USING btree (thread_id, created_at);


--
-- Name: ix_agent_action_log_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_thread_id ON public.agent_action_log USING btree (thread_id);


--
-- Name: ix_agent_action_log_tool_call_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_tool_call_id ON public.agent_action_log USING hash (tool_call_id);


--
-- Name: ix_agent_action_log_tool_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_tool_name ON public.agent_action_log USING btree (tool_name);


--
-- Name: ix_agent_action_log_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_turn_id ON public.agent_action_log USING hash (turn_id);


--
-- Name: ix_agent_action_log_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_user_id ON public.agent_action_log USING btree (user_id);


--
-- Name: ix_agent_action_log_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_action_log_workspace_id ON public.agent_action_log USING btree (workspace_id);


--
-- Name: ix_agent_permission_rules_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_permission_rules_created_at ON public.agent_permission_rules USING btree (created_at);


--
-- Name: ix_agent_permission_rules_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_permission_rules_id ON public.agent_permission_rules USING btree (id);


--
-- Name: ix_agent_permission_rules_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_permission_rules_thread_id ON public.agent_permission_rules USING btree (thread_id);


--
-- Name: ix_agent_permission_rules_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_permission_rules_user_id ON public.agent_permission_rules USING btree (user_id);


--
-- Name: ix_agent_permission_rules_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_permission_rules_workspace_id ON public.agent_permission_rules USING btree (workspace_id);


--
-- Name: ix_artifact_files_artifact_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_files_artifact_id ON public.artifact_files USING btree (artifact_id);


--
-- Name: ix_artifact_files_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_files_created_at ON public.artifact_files USING btree (created_at);


--
-- Name: ix_artifact_files_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_files_id ON public.artifact_files USING btree (id);


--
-- Name: ix_artifacts_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifacts_created_at ON public.artifacts USING btree (created_at);


--
-- Name: ix_artifacts_created_by_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifacts_created_by_id ON public.artifacts USING btree (created_by_id);


--
-- Name: ix_artifacts_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifacts_id ON public.artifacts USING btree (id);


--
-- Name: ix_artifacts_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifacts_thread_id ON public.artifacts USING btree (thread_id);


--
-- Name: ix_artifacts_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifacts_updated_at ON public.artifacts USING btree (updated_at);


--
-- Name: ix_artifacts_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifacts_workspace_id ON public.artifacts USING btree (workspace_id);


--
-- Name: ix_automation_runs_automation_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_runs_automation_id ON public.automation_runs USING btree (automation_id);


--
-- Name: ix_automation_runs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_runs_created_at ON public.automation_runs USING btree (created_at);


--
-- Name: ix_automation_runs_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_runs_id ON public.automation_runs USING btree (id);


--
-- Name: ix_automation_runs_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_runs_status ON public.automation_runs USING btree (status);


--
-- Name: ix_automation_runs_trigger_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_runs_trigger_id ON public.automation_runs USING btree (trigger_id);


--
-- Name: ix_automation_triggers_automation_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_triggers_automation_id ON public.automation_triggers USING btree (automation_id);


--
-- Name: ix_automation_triggers_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_triggers_created_at ON public.automation_triggers USING btree (created_at);


--
-- Name: ix_automation_triggers_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_triggers_enabled ON public.automation_triggers USING btree (enabled);


--
-- Name: ix_automation_triggers_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_triggers_id ON public.automation_triggers USING btree (id);


--
-- Name: ix_automation_triggers_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automation_triggers_type ON public.automation_triggers USING btree (type);


--
-- Name: ix_automations_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automations_created_at ON public.automations USING btree (created_at);


--
-- Name: ix_automations_created_by_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automations_created_by_user_id ON public.automations USING btree (created_by_user_id);


--
-- Name: ix_automations_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automations_id ON public.automations USING btree (id);


--
-- Name: ix_automations_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automations_status ON public.automations USING btree (status);


--
-- Name: ix_automations_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automations_updated_at ON public.automations USING btree (updated_at);


--
-- Name: ix_automations_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_automations_workspace_id ON public.automations USING btree (workspace_id);


--
-- Name: ix_billing_accounts_stripe_subscription_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_accounts_stripe_subscription_id ON public.billing_accounts USING btree (stripe_subscription_id);


--
-- Name: ix_billing_ledger_usage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_ledger_usage_id ON public.billing_ledger USING btree (usage_id);


--
-- Name: ix_billing_ledger_user_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_ledger_user_created ON public.billing_ledger USING btree (user_id, created_at);


--
-- Name: ix_billing_payments_user_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_payments_user_created ON public.billing_payments USING btree (user_id, created_at);


--
-- Name: ix_billing_usage_action; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_usage_action ON public.billing_usage USING btree (action);


--
-- Name: ix_billing_usage_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_usage_notebook_id ON public.billing_usage USING btree (notebook_id);


--
-- Name: ix_billing_usage_open_holds; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_usage_open_holds ON public.billing_usage USING btree (status, created_at) WHERE ((status)::text = 'reserved'::text);


--
-- Name: ix_billing_usage_user_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_billing_usage_user_created ON public.billing_usage USING btree (user_id, created_at);


--
-- Name: ix_chat_comment_mentions_comment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comment_mentions_comment_id ON public.chat_comment_mentions USING btree (comment_id);


--
-- Name: ix_chat_comment_mentions_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comment_mentions_created_at ON public.chat_comment_mentions USING btree (created_at);


--
-- Name: ix_chat_comment_mentions_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comment_mentions_id ON public.chat_comment_mentions USING btree (id);


--
-- Name: ix_chat_comment_mentions_mentioned_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comment_mentions_mentioned_user_id ON public.chat_comment_mentions USING btree (mentioned_user_id);


--
-- Name: ix_chat_comments_author_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_author_id ON public.chat_comments USING btree (author_id);


--
-- Name: ix_chat_comments_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_created_at ON public.chat_comments USING btree (created_at);


--
-- Name: ix_chat_comments_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_id ON public.chat_comments USING btree (id);


--
-- Name: ix_chat_comments_message_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_message_id ON public.chat_comments USING btree (message_id);


--
-- Name: ix_chat_comments_parent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_parent_id ON public.chat_comments USING btree (parent_id);


--
-- Name: ix_chat_comments_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_thread_id ON public.chat_comments USING btree (thread_id);


--
-- Name: ix_chat_comments_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_comments_updated_at ON public.chat_comments USING btree (updated_at);


--
-- Name: ix_chat_session_state_ai_responding_to_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_session_state_ai_responding_to_user_id ON public.chat_session_state USING btree (ai_responding_to_user_id);


--
-- Name: ix_chat_session_state_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chat_session_state_id ON public.chat_session_state USING btree (id);


--
-- Name: ix_chat_session_state_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_chat_session_state_thread_id ON public.chat_session_state USING btree (thread_id);


--
-- Name: ix_chunks_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chunks_created_at ON public.chunks USING btree (created_at);


--
-- Name: ix_chunks_document_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chunks_document_id ON public.chunks USING btree (document_id);


--
-- Name: ix_chunks_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_chunks_id ON public.chunks USING btree (id);


--
-- Name: ix_connections_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_connections_created_at ON public.connections USING btree (created_at);


--
-- Name: ix_connections_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_connections_id ON public.connections USING btree (id);


--
-- Name: ix_connections_provider; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_connections_provider ON public.connections USING btree (provider);


--
-- Name: ix_connections_scope; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_connections_scope ON public.connections USING btree (scope);


--
-- Name: ix_deliverable_jobs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_created_at ON public.deliverable_jobs USING btree (created_at);


--
-- Name: ix_deliverable_jobs_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_id ON public.deliverable_jobs USING btree (id);


--
-- Name: ix_deliverable_jobs_outbox; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_outbox ON public.deliverable_jobs USING btree (updated_at) WHERE (status = 'queued'::public.deliverable_job_status);


--
-- Name: ix_deliverable_jobs_stale_running; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_stale_running ON public.deliverable_jobs USING btree (heartbeat_at, id) WHERE (status = ANY (ARRAY['running'::public.deliverable_job_status, 'cancelling'::public.deliverable_job_status]));


--
-- Name: ix_deliverable_jobs_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_thread_id ON public.deliverable_jobs USING btree (thread_id);


--
-- Name: ix_deliverable_jobs_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_updated_at ON public.deliverable_jobs USING btree (updated_at);


--
-- Name: ix_deliverable_jobs_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_workspace_id ON public.deliverable_jobs USING btree (workspace_id);


--
-- Name: ix_deliverable_jobs_workspace_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_deliverable_jobs_workspace_status ON public.deliverable_jobs USING btree (workspace_id, status);


--
-- Name: ix_document_files_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_files_created_at ON public.document_files USING btree (created_at);


--
-- Name: ix_document_files_created_by_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_files_created_by_id ON public.document_files USING btree (created_by_id);


--
-- Name: ix_document_files_document_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_files_document_id ON public.document_files USING btree (document_id);


--
-- Name: ix_document_files_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_files_id ON public.document_files USING btree (id);


--
-- Name: ix_document_files_kind; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_files_kind ON public.document_files USING btree (kind);


--
-- Name: ix_document_files_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_files_workspace_id ON public.document_files USING btree (workspace_id);


--
-- Name: ix_document_revisions_agent_action_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_revisions_agent_action_id ON public.document_revisions USING btree (agent_action_id);


--
-- Name: ix_document_revisions_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_revisions_created_at ON public.document_revisions USING btree (created_at);


--
-- Name: ix_document_revisions_created_by_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_revisions_created_by_turn_id ON public.document_revisions USING hash (created_by_turn_id);


--
-- Name: ix_document_revisions_document_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_revisions_document_id ON public.document_revisions USING btree (document_id);


--
-- Name: ix_document_revisions_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_revisions_id ON public.document_revisions USING btree (id);


--
-- Name: ix_document_revisions_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_revisions_workspace_id ON public.document_revisions USING btree (workspace_id);


--
-- Name: ix_document_source_guides_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_source_guides_created_at ON public.document_source_guides USING btree (created_at);


--
-- Name: ix_document_source_guides_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_source_guides_id ON public.document_source_guides USING btree (id);


--
-- Name: ix_document_versions_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_versions_created_at ON public.document_versions USING btree (created_at);


--
-- Name: ix_document_versions_document_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_versions_document_id ON public.document_versions USING btree (document_id);


--
-- Name: ix_document_versions_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_document_versions_id ON public.document_versions USING btree (id);


--
-- Name: ix_documents_connector_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_connector_id ON public.documents USING btree (connector_id);


--
-- Name: ix_documents_content_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_content_hash ON public.documents USING btree (content_hash);


--
-- Name: ix_documents_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_created_at ON public.documents USING btree (created_at);


--
-- Name: ix_documents_created_by_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_created_by_id ON public.documents USING btree (created_by_id);


--
-- Name: ix_documents_folder_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_folder_id ON public.documents USING btree (folder_id);


--
-- Name: ix_documents_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_id ON public.documents USING btree (id);


--
-- Name: ix_documents_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_status ON public.documents USING btree (status);


--
-- Name: ix_documents_title; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_title ON public.documents USING btree (title);


--
-- Name: ix_documents_unique_identifier_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_documents_unique_identifier_hash ON public.documents USING btree (unique_identifier_hash);


--
-- Name: ix_documents_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_documents_updated_at ON public.documents USING btree (updated_at);


--
-- Name: ix_embedding_cache_sets_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_embedding_cache_sets_created_at ON public.embedding_cache_sets USING btree (created_at);


--
-- Name: ix_embedding_cache_sets_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_embedding_cache_sets_id ON public.embedding_cache_sets USING btree (id);


--
-- Name: ix_embedding_cache_sets_last_used_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_embedding_cache_sets_last_used_at ON public.embedding_cache_sets USING btree (last_used_at);


--
-- Name: ix_etl_cache_parses_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_etl_cache_parses_created_at ON public.etl_cache_parses USING btree (created_at);


--
-- Name: ix_etl_cache_parses_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_etl_cache_parses_id ON public.etl_cache_parses USING btree (id);


--
-- Name: ix_etl_cache_parses_last_used_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_etl_cache_parses_last_used_at ON public.etl_cache_parses USING btree (last_used_at);


--
-- Name: ix_folder_revisions_agent_action_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folder_revisions_agent_action_id ON public.folder_revisions USING btree (agent_action_id);


--
-- Name: ix_folder_revisions_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folder_revisions_created_at ON public.folder_revisions USING btree (created_at);


--
-- Name: ix_folder_revisions_created_by_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folder_revisions_created_by_turn_id ON public.folder_revisions USING hash (created_by_turn_id);


--
-- Name: ix_folder_revisions_folder_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folder_revisions_folder_id ON public.folder_revisions USING btree (folder_id);


--
-- Name: ix_folder_revisions_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folder_revisions_id ON public.folder_revisions USING btree (id);


--
-- Name: ix_folder_revisions_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folder_revisions_workspace_id ON public.folder_revisions USING btree (workspace_id);


--
-- Name: ix_folders_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_created_at ON public.folders USING btree (created_at);


--
-- Name: ix_folders_created_by_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_created_by_id ON public.folders USING btree (created_by_id);


--
-- Name: ix_folders_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_id ON public.folders USING btree (id);


--
-- Name: ix_folders_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_name ON public.folders USING btree (name);


--
-- Name: ix_folders_parent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_parent_id ON public.folders USING btree (parent_id);


--
-- Name: ix_folders_position; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_position ON public.folders USING btree ("position");


--
-- Name: ix_folders_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_updated_at ON public.folders USING btree (updated_at);


--
-- Name: ix_folders_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_folders_workspace_id ON public.folders USING btree (workspace_id);


--
-- Name: ix_logs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_logs_created_at ON public.logs USING btree (created_at);


--
-- Name: ix_logs_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_logs_id ON public.logs USING btree (id);


--
-- Name: ix_logs_level; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_logs_level ON public.logs USING btree (level);


--
-- Name: ix_logs_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_logs_source ON public.logs USING btree (source);


--
-- Name: ix_logs_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_logs_status ON public.logs USING btree (status);


--
-- Name: ix_model_compatibility_checked_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_model_compatibility_checked_at ON public.model_compatibility USING btree (checked_at);


--
-- Name: ix_model_compatibility_model_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_model_compatibility_model_id ON public.model_compatibility USING btree (model_id);


--
-- Name: ix_model_compatibility_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_model_compatibility_status ON public.model_compatibility USING btree (status);


--
-- Name: ix_models_billing_tier; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_models_billing_tier ON public.models USING btree (billing_tier);


--
-- Name: ix_models_connection_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_models_connection_id ON public.models USING btree (connection_id);


--
-- Name: ix_models_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_models_created_at ON public.models USING btree (created_at);


--
-- Name: ix_models_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_models_id ON public.models USING btree (id);


--
-- Name: ix_models_model_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_models_model_id ON public.models USING btree (model_id);


--
-- Name: ix_new_chat_messages_author_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_messages_author_id ON public.new_chat_messages USING btree (author_id);


--
-- Name: ix_new_chat_messages_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_messages_created_at ON public.new_chat_messages USING btree (created_at);


--
-- Name: ix_new_chat_messages_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_messages_id ON public.new_chat_messages USING btree (id);


--
-- Name: ix_new_chat_messages_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_messages_thread_id ON public.new_chat_messages USING btree (thread_id);


--
-- Name: ix_new_chat_messages_turn_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_messages_turn_id ON public.new_chat_messages USING btree (turn_id);


--
-- Name: ix_new_chat_threads_cloned_from_snapshot_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_cloned_from_snapshot_id ON public.new_chat_threads USING btree (cloned_from_snapshot_id);


--
-- Name: ix_new_chat_threads_cloned_from_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_cloned_from_thread_id ON public.new_chat_threads USING btree (cloned_from_thread_id);


--
-- Name: ix_new_chat_threads_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_created_at ON public.new_chat_threads USING btree (created_at);


--
-- Name: ix_new_chat_threads_created_by_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_created_by_id ON public.new_chat_threads USING btree (created_by_id);


--
-- Name: ix_new_chat_threads_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_id ON public.new_chat_threads USING btree (id);


--
-- Name: ix_new_chat_threads_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_notebook_id ON public.new_chat_threads USING btree (notebook_id);


--
-- Name: ix_new_chat_threads_title; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_title ON public.new_chat_threads USING btree (title);


--
-- Name: ix_new_chat_threads_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_updated_at ON public.new_chat_threads USING btree (updated_at);


--
-- Name: ix_new_chat_threads_visibility; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_new_chat_threads_visibility ON public.new_chat_threads USING btree (visibility);


--
-- Name: ix_notebook_earlier_chats_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_earlier_chats_notebook_id ON public.notebook_earlier_chats USING btree (notebook_id);


--
-- Name: ix_notebook_earlier_chats_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_earlier_chats_workspace_id ON public.notebook_earlier_chats USING btree (workspace_id);


--
-- Name: ix_notebook_features_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_features_id ON public.notebook_features USING btree (id);


--
-- Name: ix_notebook_features_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_features_notebook_id ON public.notebook_features USING btree (notebook_id);


--
-- Name: ix_notebook_features_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_features_user_id ON public.notebook_features USING btree (user_id);


--
-- Name: ix_notebook_notes_automation_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_notes_automation_id ON public.notebook_notes USING btree (automation_id);


--
-- Name: ix_notebook_notes_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_notes_created_at ON public.notebook_notes USING btree (created_at);


--
-- Name: ix_notebook_notes_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_notes_id ON public.notebook_notes USING btree (id);


--
-- Name: ix_notebook_notes_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_notes_notebook_id ON public.notebook_notes USING btree (notebook_id);


--
-- Name: ix_notebook_outputs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_outputs_created_at ON public.notebook_outputs USING btree (created_at);


--
-- Name: ix_notebook_outputs_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_outputs_id ON public.notebook_outputs USING btree (id);


--
-- Name: ix_notebook_outputs_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_outputs_notebook_id ON public.notebook_outputs USING btree (notebook_id);


--
-- Name: ix_notebook_outputs_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_outputs_workspace_id ON public.notebook_outputs USING btree (workspace_id);


--
-- Name: ix_notebook_public_links_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_public_links_created_at ON public.notebook_public_links USING btree (created_at);


--
-- Name: ix_notebook_public_links_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_public_links_id ON public.notebook_public_links USING btree (id);


--
-- Name: ix_notebook_public_links_share_token; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_notebook_public_links_share_token ON public.notebook_public_links USING btree (share_token);


--
-- Name: ix_notebook_public_links_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_public_links_workspace_id ON public.notebook_public_links USING btree (workspace_id);


--
-- Name: ix_notebook_report_templates_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_report_templates_created_at ON public.notebook_report_templates USING btree (created_at);


--
-- Name: ix_notebook_report_templates_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_report_templates_id ON public.notebook_report_templates USING btree (id);


--
-- Name: ix_notebook_report_templates_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_report_templates_workspace_id ON public.notebook_report_templates USING btree (workspace_id);


--
-- Name: ix_notebook_sources_document_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_sources_document_id ON public.notebook_sources USING btree (document_id);


--
-- Name: ix_notebook_sources_folder_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_sources_folder_id ON public.notebook_sources USING btree (folder_id);


--
-- Name: ix_notebook_sources_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_sources_id ON public.notebook_sources USING btree (id);


--
-- Name: ix_notebook_sources_notebook_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebook_sources_notebook_id ON public.notebook_sources USING btree (notebook_id);


--
-- Name: ix_notebooks_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebooks_created_at ON public.notebooks USING btree (created_at);


--
-- Name: ix_notebooks_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebooks_id ON public.notebooks USING btree (id);


--
-- Name: ix_notebooks_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebooks_updated_at ON public.notebooks USING btree (updated_at);


--
-- Name: ix_notebooks_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notebooks_workspace_id ON public.notebooks USING btree (workspace_id);


--
-- Name: ix_notifications_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_created_at ON public.notifications USING btree (created_at);


--
-- Name: ix_notifications_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_id ON public.notifications USING btree (id);


--
-- Name: ix_notifications_read; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_read ON public.notifications USING btree (read);


--
-- Name: ix_notifications_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_type ON public.notifications USING btree (type);


--
-- Name: ix_notifications_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_updated_at ON public.notifications USING btree (updated_at);


--
-- Name: ix_notifications_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_user_id ON public.notifications USING btree (user_id);


--
-- Name: ix_notifications_user_read_type_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_user_read_type_created ON public.notifications USING btree (user_id, read, type, created_at);


--
-- Name: ix_notifications_user_workspace_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_user_workspace_created ON public.notifications USING btree (user_id, workspace_id, created_at);


--
-- Name: ix_notifications_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_workspace_id ON public.notifications USING btree (workspace_id);


--
-- Name: ix_oauth_account_account_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_oauth_account_account_id ON public.oauth_account USING btree (account_id);


--
-- Name: ix_oauth_account_oauth_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_oauth_account_oauth_name ON public.oauth_account USING btree (oauth_name);


--
-- Name: ix_personal_access_tokens_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_personal_access_tokens_created_at ON public.personal_access_tokens USING btree (created_at);


--
-- Name: ix_personal_access_tokens_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_personal_access_tokens_expires_at ON public.personal_access_tokens USING btree (expires_at);


--
-- Name: ix_personal_access_tokens_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_personal_access_tokens_id ON public.personal_access_tokens USING btree (id);


--
-- Name: ix_personal_access_tokens_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_personal_access_tokens_token_hash ON public.personal_access_tokens USING btree (token_hash);


--
-- Name: ix_personal_access_tokens_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_personal_access_tokens_user_id ON public.personal_access_tokens USING btree (user_id);


--
-- Name: ix_podcast_runs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_podcast_runs_created_at ON public.podcast_runs USING btree (created_at);


--
-- Name: ix_podcast_runs_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_podcast_runs_id ON public.podcast_runs USING btree (id);


--
-- Name: ix_podcast_runs_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_podcast_runs_status ON public.podcast_runs USING btree (status);


--
-- Name: ix_podcast_runs_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_podcast_runs_thread_id ON public.podcast_runs USING btree (thread_id);


--
-- Name: ix_prompts_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_prompts_created_at ON public.prompts USING btree (created_at);


--
-- Name: ix_prompts_default_prompt_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_prompts_default_prompt_slug ON public.prompts USING btree (default_prompt_slug);


--
-- Name: ix_prompts_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_prompts_id ON public.prompts USING btree (id);


--
-- Name: ix_prompts_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_prompts_user_id ON public.prompts USING btree (user_id);


--
-- Name: ix_prompts_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_prompts_workspace_id ON public.prompts USING btree (workspace_id);


--
-- Name: ix_public_chat_snapshots_content_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_content_hash ON public.public_chat_snapshots USING btree (content_hash);


--
-- Name: ix_public_chat_snapshots_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_created_at ON public.public_chat_snapshots USING btree (created_at);


--
-- Name: ix_public_chat_snapshots_created_by_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_created_by_user_id ON public.public_chat_snapshots USING btree (created_by_user_id);


--
-- Name: ix_public_chat_snapshots_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_id ON public.public_chat_snapshots USING btree (id);


--
-- Name: ix_public_chat_snapshots_notebook_output_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_notebook_output_id ON public.public_chat_snapshots USING btree (notebook_output_id);


--
-- Name: ix_public_chat_snapshots_share_token; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_public_chat_snapshots_share_token ON public.public_chat_snapshots USING btree (share_token);


--
-- Name: ix_public_chat_snapshots_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_thread_id ON public.public_chat_snapshots USING btree (thread_id);


--
-- Name: ix_public_chat_snapshots_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_public_chat_snapshots_workspace_id ON public.public_chat_snapshots USING btree (workspace_id);


--
-- Name: ix_push_devices_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_push_devices_created_at ON public.push_devices USING btree (created_at);


--
-- Name: ix_push_devices_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_push_devices_id ON public.push_devices USING btree (id);


--
-- Name: ix_push_devices_session_family_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_push_devices_session_family_id ON public.push_devices USING btree (session_family_id);


--
-- Name: ix_push_devices_token; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_push_devices_token ON public.push_devices USING btree (token);


--
-- Name: ix_push_devices_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_push_devices_user_id ON public.push_devices USING btree (user_id);


--
-- Name: ix_push_tickets_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_push_tickets_created_at ON public.push_tickets USING btree (created_at);


--
-- Name: ix_push_tickets_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_push_tickets_id ON public.push_tickets USING btree (id);


--
-- Name: ix_push_tickets_ticket_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_push_tickets_ticket_id ON public.push_tickets USING btree (ticket_id);


--
-- Name: ix_refresh_tokens_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_refresh_tokens_created_at ON public.refresh_tokens USING btree (created_at);


--
-- Name: ix_refresh_tokens_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_refresh_tokens_expires_at ON public.refresh_tokens USING btree (expires_at);


--
-- Name: ix_refresh_tokens_family_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_refresh_tokens_family_id ON public.refresh_tokens USING btree (family_id);


--
-- Name: ix_refresh_tokens_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_refresh_tokens_token_hash ON public.refresh_tokens USING btree (token_hash);


--
-- Name: ix_refresh_tokens_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_refresh_tokens_user_id ON public.refresh_tokens USING btree (user_id);


--
-- Name: ix_runs_capability; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_runs_capability ON public.runs USING btree (capability);


--
-- Name: ix_runs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_runs_created_at ON public.runs USING btree (created_at);


--
-- Name: ix_runs_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_runs_user_id ON public.runs USING btree (user_id);


--
-- Name: ix_runs_workspace_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_runs_workspace_created ON public.runs USING btree (workspace_id, created_at);


--
-- Name: ix_runs_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_runs_workspace_id ON public.runs USING btree (workspace_id);


--
-- Name: ix_search_source_connector_enablements_connector_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connector_enablements_connector_id ON public.search_source_connector_enablements USING btree (connector_id);


--
-- Name: ix_search_source_connector_enablements_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connector_enablements_created_at ON public.search_source_connector_enablements USING btree (created_at);


--
-- Name: ix_search_source_connector_enablements_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connector_enablements_id ON public.search_source_connector_enablements USING btree (id);


--
-- Name: ix_search_source_connector_enablements_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connector_enablements_workspace_id ON public.search_source_connector_enablements USING btree (workspace_id);


--
-- Name: ix_search_source_connectors_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connectors_created_at ON public.search_source_connectors USING btree (created_at);


--
-- Name: ix_search_source_connectors_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connectors_id ON public.search_source_connectors USING btree (id);


--
-- Name: ix_search_source_connectors_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_search_source_connectors_name ON public.search_source_connectors USING btree (name);


--
-- Name: ix_signup_credit_claims_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_signup_credit_claims_created_at ON public.signup_credit_claims USING btree (created_at);


--
-- Name: ix_signup_credit_claims_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_signup_credit_claims_id ON public.signup_credit_claims USING btree (id);


--
-- Name: ix_site_announcements_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_site_announcements_created_at ON public.site_announcements USING btree (created_at);


--
-- Name: ix_site_announcements_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_site_announcements_id ON public.site_announcements USING btree (id);


--
-- Name: ix_token_usage_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_created_at ON public.token_usage USING btree (created_at);


--
-- Name: ix_token_usage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_id ON public.token_usage USING btree (id);


--
-- Name: ix_token_usage_message_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_message_id ON public.token_usage USING btree (message_id);


--
-- Name: ix_token_usage_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_thread_id ON public.token_usage USING btree (thread_id);


--
-- Name: ix_token_usage_usage_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_usage_type ON public.token_usage USING btree (usage_type);


--
-- Name: ix_token_usage_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_user_id ON public.token_usage USING btree (user_id);


--
-- Name: ix_token_usage_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_token_usage_workspace_id ON public.token_usage USING btree (workspace_id);


--
-- Name: ix_tool_output_spills_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tool_output_spills_created_at ON public.tool_output_spills USING btree (created_at);


--
-- Name: ix_tool_output_spills_workspace_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tool_output_spills_workspace_id ON public.tool_output_spills USING btree (workspace_id);


--
-- Name: ix_user_email; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_user_email ON public."user" USING btree (email);


--
-- Name: ix_video_presentation_runs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_video_presentation_runs_created_at ON public.video_presentation_runs USING btree (created_at);


--
-- Name: ix_video_presentation_runs_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_video_presentation_runs_id ON public.video_presentation_runs USING btree (id);


--
-- Name: ix_video_presentation_runs_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_video_presentation_runs_status ON public.video_presentation_runs USING btree (status);


--
-- Name: ix_video_presentation_runs_thread_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_video_presentation_runs_thread_id ON public.video_presentation_runs USING btree (thread_id);


--
-- Name: ix_workspace_git_remotes_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_git_remotes_created_at ON public.workspace_git_remotes USING btree (created_at);


--
-- Name: ix_workspace_git_remotes_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_git_remotes_id ON public.workspace_git_remotes USING btree (id);


--
-- Name: ix_workspace_invites_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_invites_created_at ON public.workspace_invites USING btree (created_at);


--
-- Name: ix_workspace_invites_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_invites_email ON public.workspace_invites USING btree (email);


--
-- Name: ix_workspace_invites_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_invites_id ON public.workspace_invites USING btree (id);


--
-- Name: ix_workspace_invites_invite_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_workspace_invites_invite_code ON public.workspace_invites USING btree (invite_code);


--
-- Name: ix_workspace_memberships_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_memberships_created_at ON public.workspace_memberships USING btree (created_at);


--
-- Name: ix_workspace_memberships_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_memberships_id ON public.workspace_memberships USING btree (id);


--
-- Name: ix_workspace_roles_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_roles_created_at ON public.workspace_roles USING btree (created_at);


--
-- Name: ix_workspace_roles_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_roles_id ON public.workspace_roles USING btree (id);


--
-- Name: ix_workspace_roles_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspace_roles_name ON public.workspace_roles USING btree (name);


--
-- Name: ix_workspaces_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspaces_created_at ON public.workspaces USING btree (created_at);


--
-- Name: ix_workspaces_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspaces_id ON public.workspaces USING btree (id);


--
-- Name: ix_workspaces_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workspaces_name ON public.workspaces USING btree (name);


--
-- Name: uq_account_connector_type_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_account_connector_type_name ON public.search_source_connectors USING btree (user_id, connector_type, name) WHERE (workspace_id IS NULL);


--
-- Name: uq_billing_ledger_kind_reference; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_billing_ledger_kind_reference ON public.billing_ledger USING btree (kind, reference) WHERE (reference IS NOT NULL);


--
-- Name: uq_billing_usage_reference; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_billing_usage_reference ON public.billing_usage USING btree (reference) WHERE (reference IS NOT NULL);


--
-- Name: uq_deliverable_jobs_workspace_kind_tool_call; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_deliverable_jobs_workspace_kind_tool_call ON public.deliverable_jobs USING btree (workspace_id, kind, md5(tool_call_id));


--
-- Name: uq_folder_space_parent_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_folder_space_parent_name ON public.folders USING btree (workspace_id, COALESCE(parent_id, 0), name);


--
-- Name: uq_new_chat_messages_thread_turn_role; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_new_chat_messages_thread_turn_role ON public.new_chat_messages USING btree (thread_id, turn_id, role) WHERE (turn_id IS NOT NULL);


--
-- Name: uq_token_usage_message_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_token_usage_message_id ON public.token_usage USING btree (message_id) WHERE (message_id IS NOT NULL);


--
-- Name: ux_agent_action_log_reverse_of; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ux_agent_action_log_reverse_of ON public.agent_action_log USING btree (reverse_of) WHERE (reverse_of IS NOT NULL);


--
-- Name: agent_action_log agent_action_log_reverse_of_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_action_log
    ADD CONSTRAINT agent_action_log_reverse_of_fkey FOREIGN KEY (reverse_of) REFERENCES public.agent_action_log(id) ON DELETE SET NULL;


--
-- Name: agent_action_log agent_action_log_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_action_log
    ADD CONSTRAINT agent_action_log_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: agent_action_log agent_action_log_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_action_log
    ADD CONSTRAINT agent_action_log_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: agent_action_log agent_action_log_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_action_log
    ADD CONSTRAINT agent_action_log_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: agent_permission_rules agent_permission_rules_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_permission_rules
    ADD CONSTRAINT agent_permission_rules_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: agent_permission_rules agent_permission_rules_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_permission_rules
    ADD CONSTRAINT agent_permission_rules_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: agent_permission_rules agent_permission_rules_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_permission_rules
    ADD CONSTRAINT agent_permission_rules_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: artifact_files artifact_files_artifact_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_files
    ADD CONSTRAINT artifact_files_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id) ON DELETE CASCADE;


--
-- Name: artifacts artifacts_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: artifacts artifacts_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE CASCADE;


--
-- Name: artifacts artifacts_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE SET NULL;


--
-- Name: artifacts artifacts_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifacts
    ADD CONSTRAINT artifacts_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: automation_runs automation_runs_automation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_runs
    ADD CONSTRAINT automation_runs_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES public.automations(id) ON DELETE CASCADE;


--
-- Name: automation_runs automation_runs_trigger_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_runs
    ADD CONSTRAINT automation_runs_trigger_id_fkey FOREIGN KEY (trigger_id) REFERENCES public.automation_triggers(id) ON DELETE SET NULL;


--
-- Name: automation_triggers automation_triggers_automation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_triggers
    ADD CONSTRAINT automation_triggers_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES public.automations(id) ON DELETE CASCADE;


--
-- Name: automations automations_created_by_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automations
    ADD CONSTRAINT automations_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: automations automations_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automations
    ADD CONSTRAINT automations_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: billing_accounts billing_accounts_plan_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_accounts
    ADD CONSTRAINT billing_accounts_plan_id_fkey FOREIGN KEY (plan_id) REFERENCES public.billing_plans(id);


--
-- Name: billing_accounts billing_accounts_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_accounts
    ADD CONSTRAINT billing_accounts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: billing_ledger billing_ledger_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_ledger
    ADD CONSTRAINT billing_ledger_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: billing_payments billing_payments_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_payments
    ADD CONSTRAINT billing_payments_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: billing_usage billing_usage_payer_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_usage
    ADD CONSTRAINT billing_usage_payer_user_id_fkey FOREIGN KEY (payer_user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: billing_usage billing_usage_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.billing_usage
    ADD CONSTRAINT billing_usage_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: chat_comment_mentions chat_comment_mentions_comment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comment_mentions
    ADD CONSTRAINT chat_comment_mentions_comment_id_fkey FOREIGN KEY (comment_id) REFERENCES public.chat_comments(id) ON DELETE CASCADE;


--
-- Name: chat_comment_mentions chat_comment_mentions_mentioned_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comment_mentions
    ADD CONSTRAINT chat_comment_mentions_mentioned_user_id_fkey FOREIGN KEY (mentioned_user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: chat_comments chat_comments_author_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comments
    ADD CONSTRAINT chat_comments_author_id_fkey FOREIGN KEY (author_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: chat_comments chat_comments_message_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comments
    ADD CONSTRAINT chat_comments_message_id_fkey FOREIGN KEY (message_id) REFERENCES public.new_chat_messages(id) ON DELETE CASCADE;


--
-- Name: chat_comments chat_comments_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comments
    ADD CONSTRAINT chat_comments_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.chat_comments(id) ON DELETE CASCADE;


--
-- Name: chat_comments chat_comments_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_comments
    ADD CONSTRAINT chat_comments_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: chat_session_state chat_session_state_ai_responding_to_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_session_state
    ADD CONSTRAINT chat_session_state_ai_responding_to_user_id_fkey FOREIGN KEY (ai_responding_to_user_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: chat_session_state chat_session_state_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chat_session_state
    ADD CONSTRAINT chat_session_state_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: chunks chunks_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE CASCADE;


--
-- Name: chunks chunks_document_workspace_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_document_workspace_fkey FOREIGN KEY (document_id, workspace_id) REFERENCES public.documents(id, workspace_id) ON UPDATE CASCADE ON DELETE CASCADE;


--
-- Name: chunks chunks_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chunks
    ADD CONSTRAINT chunks_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: connections connections_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connections
    ADD CONSTRAINT connections_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: connections connections_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.connections
    ADD CONSTRAINT connections_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: deliverable_jobs deliverable_jobs_artifact_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deliverable_jobs
    ADD CONSTRAINT deliverable_jobs_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id) ON DELETE SET NULL;


--
-- Name: deliverable_jobs deliverable_jobs_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deliverable_jobs
    ADD CONSTRAINT deliverable_jobs_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: deliverable_jobs deliverable_jobs_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deliverable_jobs
    ADD CONSTRAINT deliverable_jobs_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE SET NULL;


--
-- Name: deliverable_jobs deliverable_jobs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deliverable_jobs
    ADD CONSTRAINT deliverable_jobs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: document_files document_files_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_files
    ADD CONSTRAINT document_files_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: document_files document_files_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_files
    ADD CONSTRAINT document_files_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE CASCADE;


--
-- Name: document_files document_files_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_files
    ADD CONSTRAINT document_files_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: document_revisions document_revisions_agent_action_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_revisions
    ADD CONSTRAINT document_revisions_agent_action_id_fkey FOREIGN KEY (agent_action_id) REFERENCES public.agent_action_log(id) ON DELETE SET NULL;


--
-- Name: document_revisions document_revisions_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_revisions
    ADD CONSTRAINT document_revisions_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE SET NULL;


--
-- Name: document_revisions document_revisions_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_revisions
    ADD CONSTRAINT document_revisions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: document_source_guides document_source_guides_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_source_guides
    ADD CONSTRAINT document_source_guides_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE CASCADE;


--
-- Name: document_versions document_versions_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.document_versions
    ADD CONSTRAINT document_versions_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE CASCADE;


--
-- Name: documents documents_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES public.search_source_connectors(id) ON DELETE SET NULL;


--
-- Name: documents documents_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: documents documents_folder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.folders(id) ON DELETE SET NULL;


--
-- Name: documents documents_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: folder_revisions folder_revisions_agent_action_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folder_revisions
    ADD CONSTRAINT folder_revisions_agent_action_id_fkey FOREIGN KEY (agent_action_id) REFERENCES public.agent_action_log(id) ON DELETE SET NULL;


--
-- Name: folder_revisions folder_revisions_folder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folder_revisions
    ADD CONSTRAINT folder_revisions_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.folders(id) ON DELETE SET NULL;


--
-- Name: folder_revisions folder_revisions_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folder_revisions
    ADD CONSTRAINT folder_revisions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: folders folders_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders
    ADD CONSTRAINT folders_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: folders folders_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders
    ADD CONSTRAINT folders_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.folders(id) ON DELETE CASCADE;


--
-- Name: folders folders_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders
    ADD CONSTRAINT folders_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: logs logs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logs
    ADD CONSTRAINT logs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: models models_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.models
    ADD CONSTRAINT models_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.connections(id) ON DELETE CASCADE;


--
-- Name: new_chat_messages new_chat_messages_author_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_messages
    ADD CONSTRAINT new_chat_messages_author_id_fkey FOREIGN KEY (author_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: new_chat_messages new_chat_messages_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_messages
    ADD CONSTRAINT new_chat_messages_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: new_chat_threads new_chat_threads_cloned_from_snapshot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads
    ADD CONSTRAINT new_chat_threads_cloned_from_snapshot_id_fkey FOREIGN KEY (cloned_from_snapshot_id) REFERENCES public.public_chat_snapshots(id) ON DELETE SET NULL;


--
-- Name: new_chat_threads new_chat_threads_cloned_from_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads
    ADD CONSTRAINT new_chat_threads_cloned_from_thread_id_fkey FOREIGN KEY (cloned_from_thread_id) REFERENCES public.new_chat_threads(id) ON DELETE SET NULL;


--
-- Name: new_chat_threads new_chat_threads_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads
    ADD CONSTRAINT new_chat_threads_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: new_chat_threads new_chat_threads_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads
    ADD CONSTRAINT new_chat_threads_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE SET NULL;


--
-- Name: new_chat_threads new_chat_threads_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.new_chat_threads
    ADD CONSTRAINT new_chat_threads_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: notebook_earlier_chats notebook_earlier_chats_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_earlier_chats
    ADD CONSTRAINT notebook_earlier_chats_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE CASCADE;


--
-- Name: notebook_earlier_chats notebook_earlier_chats_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_earlier_chats
    ADD CONSTRAINT notebook_earlier_chats_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: notebook_earlier_chats notebook_earlier_chats_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_earlier_chats
    ADD CONSTRAINT notebook_earlier_chats_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: notebook_features notebook_features_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_features
    ADD CONSTRAINT notebook_features_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE CASCADE;


--
-- Name: notebook_features notebook_features_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_features
    ADD CONSTRAINT notebook_features_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: notebook_notes notebook_notes_automation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_notes
    ADD CONSTRAINT notebook_notes_automation_id_fkey FOREIGN KEY (automation_id) REFERENCES public.automations(id) ON DELETE SET NULL;


--
-- Name: notebook_notes notebook_notes_converted_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_notes
    ADD CONSTRAINT notebook_notes_converted_document_id_fkey FOREIGN KEY (converted_document_id) REFERENCES public.documents(id) ON DELETE SET NULL;


--
-- Name: notebook_notes notebook_notes_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_notes
    ADD CONSTRAINT notebook_notes_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: notebook_notes notebook_notes_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_notes
    ADD CONSTRAINT notebook_notes_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE CASCADE;


--
-- Name: notebook_outputs notebook_outputs_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: notebook_outputs notebook_outputs_deliverable_job_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_deliverable_job_id_fkey FOREIGN KEY (deliverable_job_id) REFERENCES public.deliverable_jobs(id) ON DELETE SET NULL;


--
-- Name: notebook_outputs notebook_outputs_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE CASCADE;


--
-- Name: notebook_outputs notebook_outputs_podcast_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_podcast_id_fkey FOREIGN KEY (podcast_id) REFERENCES public.podcast_runs(id) ON DELETE SET NULL;


--
-- Name: notebook_outputs notebook_outputs_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE SET NULL;


--
-- Name: notebook_outputs notebook_outputs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_outputs
    ADD CONSTRAINT notebook_outputs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: notebook_public_links notebook_public_links_created_by_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_public_links
    ADD CONSTRAINT notebook_public_links_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: notebook_public_links notebook_public_links_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_public_links
    ADD CONSTRAINT notebook_public_links_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE CASCADE;


--
-- Name: notebook_public_links notebook_public_links_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_public_links
    ADD CONSTRAINT notebook_public_links_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: notebook_report_templates notebook_report_templates_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_report_templates
    ADD CONSTRAINT notebook_report_templates_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: notebook_sources notebook_sources_document_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources
    ADD CONSTRAINT notebook_sources_document_id_fkey FOREIGN KEY (document_id) REFERENCES public.documents(id) ON DELETE CASCADE;


--
-- Name: notebook_sources notebook_sources_folder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources
    ADD CONSTRAINT notebook_sources_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.folders(id) ON DELETE CASCADE;


--
-- Name: notebook_sources notebook_sources_notebook_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebook_sources
    ADD CONSTRAINT notebook_sources_notebook_id_fkey FOREIGN KEY (notebook_id) REFERENCES public.notebooks(id) ON DELETE CASCADE;


--
-- Name: notebooks notebooks_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebooks
    ADD CONSTRAINT notebooks_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: notebooks notebooks_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notebooks
    ADD CONSTRAINT notebooks_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: notifications notifications_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: notifications notifications_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: oauth_account oauth_account_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_account
    ADD CONSTRAINT oauth_account_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: personal_access_tokens personal_access_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_access_tokens
    ADD CONSTRAINT personal_access_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: podcast_runs podcast_runs_artifact_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.podcast_runs
    ADD CONSTRAINT podcast_runs_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id) ON DELETE SET NULL;


--
-- Name: podcast_runs podcast_runs_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.podcast_runs
    ADD CONSTRAINT podcast_runs_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE SET NULL;


--
-- Name: podcast_runs podcast_runs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.podcast_runs
    ADD CONSTRAINT podcast_runs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: prompts prompts_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompts
    ADD CONSTRAINT prompts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: prompts prompts_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompts
    ADD CONSTRAINT prompts_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: public_chat_snapshots public_chat_snapshots_created_by_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots
    ADD CONSTRAINT public_chat_snapshots_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: public_chat_snapshots public_chat_snapshots_notebook_output_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots
    ADD CONSTRAINT public_chat_snapshots_notebook_output_id_fkey FOREIGN KEY (notebook_output_id) REFERENCES public.notebook_outputs(id) ON DELETE CASCADE;


--
-- Name: public_chat_snapshots public_chat_snapshots_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots
    ADD CONSTRAINT public_chat_snapshots_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: public_chat_snapshots public_chat_snapshots_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.public_chat_snapshots
    ADD CONSTRAINT public_chat_snapshots_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: push_devices push_devices_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_devices
    ADD CONSTRAINT push_devices_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: push_preferences push_preferences_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.push_preferences
    ADD CONSTRAINT push_preferences_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: refresh_tokens refresh_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: runs runs_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runs
    ADD CONSTRAINT runs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: runs runs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.runs
    ADD CONSTRAINT runs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: search_source_connector_enablements search_source_connector_enablements_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connector_enablements
    ADD CONSTRAINT search_source_connector_enablements_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES public.search_source_connectors(id) ON DELETE CASCADE;


--
-- Name: search_source_connector_enablements search_source_connector_enablements_enabled_by_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connector_enablements
    ADD CONSTRAINT search_source_connector_enablements_enabled_by_user_id_fkey FOREIGN KEY (enabled_by_user_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: search_source_connector_enablements search_source_connector_enablements_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connector_enablements
    ADD CONSTRAINT search_source_connector_enablements_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: search_source_connectors search_source_connectors_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connectors
    ADD CONSTRAINT search_source_connectors_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: search_source_connectors search_source_connectors_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.search_source_connectors
    ADD CONSTRAINT search_source_connectors_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: site_announcements site_announcements_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_announcements
    ADD CONSTRAINT site_announcements_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: site_announcements site_announcements_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_announcements
    ADD CONSTRAINT site_announcements_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: token_usage token_usage_message_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_usage
    ADD CONSTRAINT token_usage_message_id_fkey FOREIGN KEY (message_id) REFERENCES public.new_chat_messages(id) ON DELETE SET NULL;


--
-- Name: token_usage token_usage_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_usage
    ADD CONSTRAINT token_usage_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE CASCADE;


--
-- Name: token_usage token_usage_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_usage
    ADD CONSTRAINT token_usage_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: token_usage token_usage_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_usage
    ADD CONSTRAINT token_usage_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: tool_output_spills tool_output_spills_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_output_spills
    ADD CONSTRAINT tool_output_spills_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: video_presentation_runs video_presentation_runs_artifact_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_presentation_runs
    ADD CONSTRAINT video_presentation_runs_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES public.artifacts(id) ON DELETE SET NULL;


--
-- Name: video_presentation_runs video_presentation_runs_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_presentation_runs
    ADD CONSTRAINT video_presentation_runs_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.new_chat_threads(id) ON DELETE SET NULL;


--
-- Name: video_presentation_runs video_presentation_runs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_presentation_runs
    ADD CONSTRAINT video_presentation_runs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspace_git_remotes workspace_git_remotes_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_git_remotes
    ADD CONSTRAINT workspace_git_remotes_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspace_invites workspace_invites_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_invites
    ADD CONSTRAINT workspace_invites_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public."user"(id) ON DELETE SET NULL;


--
-- Name: workspace_invites workspace_invites_role_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_invites
    ADD CONSTRAINT workspace_invites_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.workspace_roles(id) ON DELETE SET NULL;


--
-- Name: workspace_invites workspace_invites_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_invites
    ADD CONSTRAINT workspace_invites_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspace_memberships workspace_memberships_invited_by_invite_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships
    ADD CONSTRAINT workspace_memberships_invited_by_invite_id_fkey FOREIGN KEY (invited_by_invite_id) REFERENCES public.workspace_invites(id) ON DELETE SET NULL;


--
-- Name: workspace_memberships workspace_memberships_role_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships
    ADD CONSTRAINT workspace_memberships_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.workspace_roles(id) ON DELETE SET NULL;


--
-- Name: workspace_memberships workspace_memberships_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships
    ADD CONSTRAINT workspace_memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- Name: workspace_memberships workspace_memberships_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_memberships
    ADD CONSTRAINT workspace_memberships_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspace_quick_models workspace_quick_models_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_quick_models
    ADD CONSTRAINT workspace_quick_models_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspace_roles workspace_roles_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_roles
    ADD CONSTRAINT workspace_roles_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspace_studio_models workspace_studio_models_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_studio_models
    ADD CONSTRAINT workspace_studio_models_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;


--
-- Name: workspaces workspaces_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspaces
    ADD CONSTRAINT workspaces_user_id_fkey FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--


