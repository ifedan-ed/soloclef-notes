--
-- PostgreSQL database dump
--

\restrict vTNc7wEwMWgx42kJOlLBqKSjQcbdcx3fYtpYCy82y9mj4btFJiMzqMaf6TK17X5

-- Dumped from database version 17.11 (Debian 17.11-1.pgdg12+2)
-- Dumped by pg_dump version 17.11 (Debian 17.11-1.pgdg12+2)

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
-- Data for Name: models; Type: TABLE DATA; Schema: public; Owner: surfsense
--

COPY public.models (connection_id, model_id, display_name, source, supports_chat, max_input_tokens, supports_image_input, supports_tools, supports_image_generation, capabilities_override, enabled, billing_tier, catalog, id, created_at) FROM stdin;
2	google-gemma-4-31b-it	google-gemma-4-31b-it	DISCOVERED	t	262144	t	t	f	{}	f	\N	{"id": "google-gemma-4-31b-it", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 262144, "max_output_tokens": 32768}	74	2026-09-20 22:00:19.509692+00
1	openai-gpt-6-luna	openai-gpt-6-luna	DISCOVERED	t	1050000	t	t	f	{}	t	premium	{"id": "openai-gpt-6-luna", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1050000, "max_output_tokens": 128000}	162	2026-09-24 02:30:39.025645+00
2	openai-gpt-6-luna	openai-gpt-6-luna	DISCOVERED	t	1050000	t	t	f	{}	t	premium	{"id": "openai-gpt-6-luna", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1050000, "max_output_tokens": 128000}	163	2026-09-24 02:30:39.103773+00
1	openrouter-kimi-k3	openrouter-kimi-k3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-kimi-k3", "object": "model", "created": 1677610602, "owned_by": "openai"}	90	2026-09-23 16:03:56.840033+00
1	openrouter-glm-4.7-flash	openrouter-glm-4.7-flash	DISCOVERED	t	200000	t	t	f	{}	f	\N	{"id": "openrouter-glm-4.7-flash", "object": "model", "created": 1677610602, "owned_by": "openai"}	92	2026-09-23 16:03:56.840045+00
2	openrouter-kimi-k3	openrouter-kimi-k3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-kimi-k3", "object": "model", "created": 1677610602, "owned_by": "openai"}	7	2026-09-20 22:00:19.50965+00
1	openrouter-mai-image-2.5-pro	openrouter-mai-image-2.5-pro	DISCOVERED	f	\N	f	f	t	{}	f	standard	{"id": "openrouter-mai-image-2.5-pro", "object": "model", "created": 1677610602, "owned_by": "openai"}	110	2026-09-23 16:03:56.840057+00
1	openrouter-qwen3.8-flash	openrouter-qwen3.8-flash	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-qwen3.8-flash", "object": "model", "created": 1677610602, "owned_by": "openai"}	108	2026-09-23 16:03:56.840056+00
1	openrouter-grok-imagine-image-2	openrouter-grok-imagine-image-2	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-grok-imagine-image-2", "object": "model", "created": 1677610602, "owned_by": "openai"}	109	2026-09-23 16:03:56.840056+00
1	ds-deepseek-v4.1-flash	ds-deepseek-v4.1-flash	DISCOVERED	t	1000000	t	t	f	{}	t	standard	{"id": "ds-deepseek-v4.1-flash", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1000000, "max_output_tokens": 384000}	2	2026-09-20 21:42:42.56492+00
1	openai-gpt-5.6-luna	openai-gpt-5.6-luna	DISCOVERED	t	922000	t	t	f	{}	t	standard	{"id": "openai-gpt-5.6-luna", "object": "model", "created": 1677610602, "owned_by": "openai"}	3	2026-09-20 21:42:42.564921+00
1	openai-gpt-image-2.5-sunburst	openai-gpt-image-2.5-sunburst	DISCOVERED	f	\N	f	f	t	{}	t	standard	{"id": "openai-gpt-image-2.5-sunburst", "object": "model", "created": 1677610602, "owned_by": "openai"}	5	2026-09-20 21:42:42.564923+00
1	openrouter-bge-m3	openrouter-bge-m3	DISCOVERED	f	\N	f	f	f	{}	t	\N	{"id": "openrouter-bge-m3", "object": "model", "created": 1677610602, "owned_by": "openai"}	1	2026-09-20 21:42:42.564913+00
1	ds-deepseek-r1	ds-deepseek-r1	DISCOVERED	t	131072	f	f	f	{}	f	\N	{"id": "ds-deepseek-r1", "object": "model", "created": 1677610602, "owned_by": "openai"}	131	2026-09-23 16:03:56.84007+00
1	alibaba-qwen3.7-max	alibaba-qwen3.7-max	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "alibaba-qwen3.7-max", "object": "model", "created": 1677610602, "owned_by": "openai"}	132	2026-09-23 16:03:56.840071+00
1	alibaba-qwen3.6-flash	alibaba-qwen3.6-flash	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "alibaba-qwen3.6-flash", "object": "model", "created": 1677610602, "owned_by": "openai"}	133	2026-09-23 16:03:56.840071+00
1	openrouter-glm-5.3	openrouter-glm-5.3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-glm-5.3", "object": "model", "created": 1677610602, "owned_by": "openai"}	134	2026-09-23 16:03:56.840072+00
1	openrouter-gemma-4-31b-it	openrouter-gemma-4-31b-it	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-gemma-4-31b-it", "object": "model", "created": 1677610602, "owned_by": "openai"}	135	2026-09-23 16:03:56.840073+00
1	openrouter-minimax-m3	openrouter-minimax-m3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-minimax-m3", "object": "model", "created": 1677610602, "owned_by": "openai"}	136	2026-09-23 16:03:56.840073+00
1	openrouter-mai-transcribe-2	openrouter-mai-transcribe-2	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-mai-transcribe-2", "object": "model", "created": 1677610602, "owned_by": "openai"}	137	2026-09-23 16:03:56.840074+00
1	openrouter-whisper-large-v3-turbo	openrouter-whisper-large-v3-turbo	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-whisper-large-v3-turbo", "object": "model", "created": 1677610602, "owned_by": "openai"}	138	2026-09-23 16:03:56.840074+00
1	google-gemma-4-31b-it	google-gemma-4-31b-it	DISCOVERED	t	262144	t	t	f	{}	f	\N	{"id": "google-gemma-4-31b-it", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 262144, "max_output_tokens": 32768}	143	2026-09-23 16:03:56.840078+00
1	google-gemini-3.1-flash-image	google-gemini-3.1-flash-image	DISCOVERED	f	65536	t	f	t	{}	f	standard	{"id": "google-gemini-3.1-flash-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	145	2026-09-23 16:03:56.840079+00
1	google-gemini-2.5-flash-image	google-gemini-2.5-flash-image	DISCOVERED	f	32768	t	t	t	{}	f	\N	{"id": "google-gemini-2.5-flash-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	144	2026-09-23 16:03:56.840078+00
2	ds-deepseek-r1	ds-deepseek-r1	DISCOVERED	t	131072	f	f	f	{}	f	\N	{"id": "ds-deepseek-r1", "object": "model", "created": 1677610602, "owned_by": "openai"}	62	2026-09-20 22:00:19.509685+00
1	openai-gpt-image-2	openai-gpt-image-2	DISCOVERED	f	\N	t	f	t	{}	f	standard	{"id": "openai-gpt-image-2", "object": "model", "created": 1677610602, "owned_by": "openai"}	148	2026-09-23 16:03:56.840081+00
1	google-gemini-3.1-flash-lite-image	google-gemini-3.1-flash-lite-image	DISCOVERED	f	65536	t	t	t	{}	f	standard	{"id": "google-gemini-3.1-flash-lite-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	146	2026-09-23 16:03:56.840079+00
1	google-gemini-3-pro-image	google-gemini-3-pro-image	DISCOVERED	f	65536	t	f	t	{}	f	standard	{"id": "google-gemini-3-pro-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	147	2026-09-23 16:03:56.84008+00
1	openai-tts-1	openai-tts-1	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openai-tts-1", "object": "model", "created": 1677610602, "owned_by": "openai"}	152	2026-09-23 16:03:56.840083+00
1	openai-gpt-image-1-mini	openai-gpt-image-1-mini	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openai-gpt-image-1-mini", "object": "model", "created": 1677610602, "owned_by": "openai"}	149	2026-09-23 16:03:56.840081+00
1	openai-gpt-image-2.5-flare	openai-gpt-image-2.5-flare	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openai-gpt-image-2.5-flare", "object": "model", "created": 1677610602, "owned_by": "openai"}	150	2026-09-23 16:03:56.840082+00
1	openai-gpt-4o-mini-tts	openai-gpt-4o-mini-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openai-gpt-4o-mini-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	151	2026-09-23 16:03:56.840083+00
1	openai-tts-1-hd	openai-tts-1-hd	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openai-tts-1-hd", "object": "model", "created": 1677610602, "owned_by": "openai"}	153	2026-09-23 16:03:56.840084+00
1	openrouter-grok-voice-tts-1.0	openrouter-grok-voice-tts-1.0	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-grok-voice-tts-1.0", "object": "model", "created": 1677610602, "owned_by": "openai"}	154	2026-09-23 16:03:56.840084+00
1	openrouter-fish-s2.1-pro-free-tts	openrouter-fish-s2.1-pro-free-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-fish-s2.1-pro-free-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	155	2026-09-23 16:03:56.840085+00
1	google-gemini-2.5-flash-preview-tts	google-gemini-2.5-flash-preview-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "google-gemini-2.5-flash-preview-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	156	2026-09-23 16:03:56.840085+00
1	openrouter-llama-3.3-70b-instruct	openrouter-llama-3.3-70b-instruct	DISCOVERED	t	\N	f	t	f	{}	f	standard	{"id": "openrouter-llama-3.3-70b-instruct", "object": "model", "created": 1677610602, "owned_by": "openai"}	95	2026-09-23 16:03:56.840047+00
1	openrouter-nova-premier-v1	openrouter-nova-premier-v1	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-nova-premier-v1", "object": "model", "created": 1677610602, "owned_by": "openai"}	93	2026-09-23 16:03:56.840046+00
1	openrouter-claude-sonnet-4	openrouter-claude-sonnet-4	DISCOVERED	t	1000000	t	t	f	{}	f	\N	{"id": "openrouter-claude-sonnet-4", "mode": "chat", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 200000, "max_output_tokens": 64000}	94	2026-09-23 16:03:56.840046+00
2	openrouter-nova-premier-v1	openrouter-nova-premier-v1	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-nova-premier-v1", "object": "model", "created": 1677610602, "owned_by": "openai"}	10	2026-09-20 22:00:19.509659+00
2	openrouter-llama-4-maverick	openrouter-llama-4-maverick	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-llama-4-maverick", "object": "model", "created": 1677610602, "owned_by": "openai"}	14	2026-09-20 22:00:19.509661+00
2	openrouter-phi-4	openrouter-phi-4	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-phi-4", "object": "model", "created": 1677610602, "owned_by": "openai"}	16	2026-09-20 22:00:19.509662+00
2	openrouter-minimax-m2.7	openrouter-minimax-m2.7	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-minimax-m2.7", "object": "model", "created": 1677610602, "owned_by": "openai"}	17	2026-09-20 22:00:19.509662+00
1	openrouter-llama-4-maverick	openrouter-llama-4-maverick	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-llama-4-maverick", "object": "model", "created": 1677610602, "owned_by": "openai"}	96	2026-09-23 16:03:56.840048+00
2	openrouter-mistral-medium-3	openrouter-mistral-medium-3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-mistral-medium-3", "object": "model", "created": 1677610602, "owned_by": "openai"}	19	2026-09-20 22:00:19.509663+00
2	openrouter-mistral-medium-3.1	openrouter-mistral-medium-3.1	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-mistral-medium-3.1", "object": "model", "created": 1677610602, "owned_by": "openai"}	20	2026-09-20 22:00:19.509664+00
1	openrouter-llama-4-scout	openrouter-llama-4-scout	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-llama-4-scout", "object": "model", "created": 1677610602, "owned_by": "openai"}	97	2026-09-23 16:03:56.840048+00
1	openrouter-phi-4	openrouter-phi-4	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-phi-4", "object": "model", "created": 1677610602, "owned_by": "openai"}	98	2026-09-23 16:03:56.840049+00
1	openrouter-minimax-m2.7	openrouter-minimax-m2.7	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-minimax-m2.7", "object": "model", "created": 1677610602, "owned_by": "openai"}	99	2026-09-23 16:03:56.84005+00
1	openrouter-mistral-large-2512	openrouter-mistral-large-2512	DISCOVERED	t	262144	t	t	f	{}	f	\N	{"id": "openrouter-mistral-large-2512", "object": "model", "created": 1677610602, "owned_by": "openai"}	100	2026-09-23 16:03:56.84005+00
1	openrouter-mistral-medium-3	openrouter-mistral-medium-3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-mistral-medium-3", "object": "model", "created": 1677610602, "owned_by": "openai"}	101	2026-09-23 16:03:56.840051+00
2	openrouter-l3.3-euryale-70b	openrouter-l3.3-euryale-70b	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-l3.3-euryale-70b", "object": "model", "created": 1677610602, "owned_by": "openai"}	26	2026-09-20 22:00:19.509667+00
2	openrouter-qwen3.8-max	openrouter-qwen3.8-max	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-qwen3.8-max", "object": "model", "created": 1677610602, "owned_by": "openai"}	27	2026-09-20 22:00:19.509667+00
1	openrouter-mistral-medium-3.1	openrouter-mistral-medium-3.1	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-mistral-medium-3.1", "object": "model", "created": 1677610602, "owned_by": "openai"}	102	2026-09-23 16:03:56.840052+00
1	openrouter-gpt-oss-120b	openrouter-gpt-oss-120b	DISCOVERED	t	131072	f	t	f	{}	f	\N	{"id": "openrouter-gpt-oss-120b", "object": "model", "created": 1677610602, "owned_by": "openai"}	103	2026-09-23 16:03:56.840052+00
1	openrouter-gpt-oss-20b	openrouter-gpt-oss-20b	DISCOVERED	t	131072	f	t	f	{}	f	\N	{"id": "openrouter-gpt-oss-20b", "object": "model", "created": 1677610602, "owned_by": "openai"}	104	2026-09-23 16:03:56.840053+00
1	openrouter-auto	openrouter-auto	DISCOVERED	t	2000000	t	t	f	{}	f	\N	{"id": "openrouter-auto", "object": "model", "created": 1677610602, "owned_by": "openai"}	105	2026-09-23 16:03:56.840054+00
1	openrouter-l3.3-euryale-70b	openrouter-l3.3-euryale-70b	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-l3.3-euryale-70b", "object": "model", "created": 1677610602, "owned_by": "openai"}	106	2026-09-23 16:03:56.840054+00
1	openrouter-qwen3.8-max	openrouter-qwen3.8-max	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-qwen3.8-max", "object": "model", "created": 1677610602, "owned_by": "openai"}	107	2026-09-23 16:03:56.840055+00
2	openrouter-llama-4-scout	openrouter-llama-4-scout	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-llama-4-scout", "object": "model", "created": 1677610602, "owned_by": "openai"}	15	2026-09-20 22:00:19.509661+00
2	openrouter-qwen3.8-flash	openrouter-qwen3.8-flash	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-qwen3.8-flash", "object": "model", "created": 1677610602, "owned_by": "openai"}	28	2026-09-20 22:00:19.509668+00
1	openrouter-mai-image-2.5	openrouter-mai-image-2.5	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-mai-image-2.5", "object": "model", "created": 1677610602, "owned_by": "openai"}	111	2026-09-23 16:03:56.840057+00
1	openrouter-grok-imagine-image-quality	openrouter-grok-imagine-image-quality	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-grok-imagine-image-quality", "object": "model", "created": 1677610602, "owned_by": "openai"}	112	2026-09-23 16:03:56.840058+00
2	google-gemini-3.1-flash-tts-preview	google-gemini-3.1-flash-tts-preview	DISCOVERED	f	8192	f	f	f	{}	f	\N	{"id": "google-gemini-3.1-flash-tts-preview", "object": "model", "created": 1677610602, "owned_by": "openai"}	89	2026-09-20 22:00:19.5097+00
2	openrouter-llama-3.3-70b-instruct	openrouter-llama-3.3-70b-instruct	DISCOVERED	t	\N	f	t	f	{}	t	standard	{"id": "openrouter-llama-3.3-70b-instruct", "object": "model", "created": 1677610602, "owned_by": "openai"}	13	2026-09-20 22:00:19.509661+00
1	openrouter-recraft-v4-pro	openrouter-recraft-v4-pro	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-recraft-v4-pro", "object": "model", "created": 1677610602, "owned_by": "openai"}	113	2026-09-23 16:03:56.840059+00
2	groq-compound	groq-compound	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "groq-compound", "object": "model", "created": 1677610602, "owned_by": "openai"}	46	2026-09-20 22:00:19.509676+00
2	groq-compound-mini	groq-compound-mini	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "groq-compound-mini", "object": "model", "created": 1677610602, "owned_by": "openai"}	47	2026-09-20 22:00:19.509677+00
2	groq-qwen3.8-27b	groq-qwen3.8-27b	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "groq-qwen3.8-27b", "object": "model", "created": 1677610602, "owned_by": "openai"}	48	2026-09-20 22:00:19.509677+00
1	openrouter-recraft-v4	openrouter-recraft-v4	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-recraft-v4", "object": "model", "created": 1677610602, "owned_by": "openai"}	114	2026-09-23 16:03:56.840059+00
1	openrouter-recraft-v3	openrouter-recraft-v3	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-recraft-v3", "object": "model", "created": 1677610602, "owned_by": "openai"}	115	2026-09-23 16:03:56.84006+00
1	openrouter-free-auto	openrouter-free-auto	DISCOVERED	t	200000	t	t	f	{}	f	\N	{"id": "openrouter-free-auto", "object": "model", "created": 1677610602, "owned_by": "openai"}	116	2026-09-23 16:03:56.840061+00
1	groq-compound	groq-compound	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "groq-compound", "object": "model", "created": 1677610602, "owned_by": "openai"}	117	2026-09-23 16:03:56.840061+00
1	groq-compound-mini	groq-compound-mini	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "groq-compound-mini", "object": "model", "created": 1677610602, "owned_by": "openai"}	118	2026-09-23 16:03:56.840062+00
1	groq-qwen3.8-27b	groq-qwen3.8-27b	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "groq-qwen3.8-27b", "object": "model", "created": 1677610602, "owned_by": "openai"}	119	2026-09-23 16:03:56.840063+00
1	groq-whisper-large-v3	groq-whisper-large-v3	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-whisper-large-v3", "object": "model", "created": 1677610602, "owned_by": "openai"}	120	2026-09-23 16:03:56.840063+00
1	groq-whisper-large-v3-turbo	groq-whisper-large-v3-turbo	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-whisper-large-v3-turbo", "object": "model", "created": 1677610602, "owned_by": "openai"}	121	2026-09-23 16:03:56.840064+00
2	jina-reranker-v2-base-multilingual	jina-reranker-v2-base-multilingual	DISCOVERED	f	1024	f	f	f	{}	f	\N	{"id": "jina-reranker-v2-base-multilingual", "mode": "rerank", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1024, "max_output_tokens": 1024}	58	2026-09-20 22:00:19.509682+00
1	local-kokoro-tts	local-kokoro-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "local-kokoro-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	123	2026-09-23 16:03:56.840065+00
1	groq-orpheus-english	groq-orpheus-english	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-orpheus-english", "object": "model", "created": 1677610602, "owned_by": "openai"}	124	2026-09-23 16:03:56.840066+00
1	groq-orpheus-arabic-saudi	groq-orpheus-arabic-saudi	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-orpheus-arabic-saudi", "object": "model", "created": 1677610602, "owned_by": "openai"}	125	2026-09-23 16:03:56.840066+00
2	alibaba-qwen3.7-max	alibaba-qwen3.7-max	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "alibaba-qwen3.7-max", "object": "model", "created": 1677610602, "owned_by": "openai"}	63	2026-09-20 22:00:19.509686+00
2	alibaba-qwen3.6-flash	alibaba-qwen3.6-flash	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "alibaba-qwen3.6-flash", "object": "model", "created": 1677610602, "owned_by": "openai"}	64	2026-09-20 22:00:19.509687+00
2	openrouter-glm-5.3	openrouter-glm-5.3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-glm-5.3", "object": "model", "created": 1677610602, "owned_by": "openai"}	65	2026-09-20 22:00:19.509687+00
2	openrouter-gemma-4-31b-it	openrouter-gemma-4-31b-it	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-gemma-4-31b-it", "object": "model", "created": 1677610602, "owned_by": "openai"}	66	2026-09-20 22:00:19.509688+00
2	openrouter-minimax-m3	openrouter-minimax-m3	DISCOVERED	t	\N	f	f	f	{}	f	\N	{"id": "openrouter-minimax-m3", "object": "model", "created": 1677610602, "owned_by": "openai"}	67	2026-09-20 22:00:19.509688+00
1	mistral-large	mistral-large	DISCOVERED	t	262144	t	t	f	{}	f	\N	{"id": "mistral-large", "object": "model", "created": 1677610602, "owned_by": "openai"}	126	2026-09-23 16:03:56.840067+00
1	jina-reranker-v2-base-multilingual	jina-reranker-v2-base-multilingual	DISCOVERED	f	1024	f	f	f	{}	f	\N	{"id": "jina-reranker-v2-base-multilingual", "mode": "rerank", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1024, "max_output_tokens": 1024}	128	2026-09-23 16:03:56.840068+00
1	cohere-rerank-v4.0-pro	cohere-rerank-v4.0-pro	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "cohere-rerank-v4.0-pro", "object": "model", "created": 1677610602, "owned_by": "openai"}	129	2026-09-23 16:03:56.840069+00
1	cohere-rerank-v4.0-fast	cohere-rerank-v4.0-fast	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "cohere-rerank-v4.0-fast", "object": "model", "created": 1677610602, "owned_by": "openai"}	130	2026-09-23 16:03:56.840069+00
1	openrouter-fish-s2.1-pro-tts	openrouter-fish-s2.1-pro-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-fish-s2.1-pro-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	139	2026-09-23 16:03:56.840075+00
1	local-bge-m3	local-bge-m3	DISCOVERED	f	8192	f	f	f	{}	f	\N	{"id": "local-bge-m3", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 8192}	122	2026-09-23 16:03:56.840065+00
1	mistral-medium	mistral-medium	DISCOVERED	t	262144	t	t	f	{}	f	standard	{"id": "mistral-medium", "object": "model", "created": 1677610602, "owned_by": "openai"}	127	2026-09-23 16:03:56.840068+00
2	openrouter-mistral-large-2512	openrouter-mistral-large-2512	DISCOVERED	t	262144	t	t	f	{}	f	\N	{"id": "openrouter-mistral-large-2512", "object": "model", "created": 1677610602, "owned_by": "openai"}	18	2026-09-20 22:00:19.509663+00
1	google-gemini-2.5-pro-preview-tts	google-gemini-2.5-pro-preview-tts	DISCOVERED	f	1048576	t	t	f	{}	f	\N	{"id": "google-gemini-2.5-pro-preview-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	157	2026-09-23 16:03:56.840086+00
1	google-gemini-3.1-pro-preview	google-gemini-3.1-pro-preview	DISCOVERED	t	1048576	t	t	f	{}	f	premium	{"id": "google-gemini-3.1-pro-preview", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1048576, "max_output_tokens": 65536}	140	2026-09-23 16:03:56.840076+00
1	google-gemini-3.1-flash-tts-preview	google-gemini-3.1-flash-tts-preview	DISCOVERED	f	8192	f	f	f	{}	f	\N	{"id": "google-gemini-3.1-flash-tts-preview", "object": "model", "created": 1677610602, "owned_by": "openai"}	158	2026-09-23 16:03:56.840087+00
2	openrouter-glm-4.7-flash	openrouter-glm-4.7-flash	DISCOVERED	t	200000	t	t	f	{}	f	\N	{"id": "openrouter-glm-4.7-flash", "object": "model", "created": 1677610602, "owned_by": "openai"}	9	2026-09-20 22:00:19.509659+00
2	openrouter-claude-sonnet-4	openrouter-claude-sonnet-4	DISCOVERED	t	1000000	t	t	f	{}	f	\N	{"id": "openrouter-claude-sonnet-4", "mode": "chat", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 200000, "max_output_tokens": 64000}	11	2026-09-20 22:00:19.50966+00
2	openrouter-gpt-oss-120b	openrouter-gpt-oss-120b	DISCOVERED	t	131072	f	t	f	{}	f	\N	{"id": "openrouter-gpt-oss-120b", "object": "model", "created": 1677610602, "owned_by": "openai"}	23	2026-09-20 22:00:19.509665+00
2	openrouter-gpt-oss-20b	openrouter-gpt-oss-20b	DISCOVERED	t	131072	f	t	f	{}	f	\N	{"id": "openrouter-gpt-oss-20b", "object": "model", "created": 1677610602, "owned_by": "openai"}	24	2026-09-20 22:00:19.509666+00
2	openrouter-auto	openrouter-auto	DISCOVERED	t	2000000	t	t	f	{}	f	\N	{"id": "openrouter-auto", "object": "model", "created": 1677610602, "owned_by": "openai"}	25	2026-09-20 22:00:19.509666+00
2	openrouter-grok-imagine-image-2	openrouter-grok-imagine-image-2	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-grok-imagine-image-2", "object": "model", "created": 1677610602, "owned_by": "openai"}	29	2026-09-20 22:00:19.509668+00
2	openrouter-mai-image-2.5	openrouter-mai-image-2.5	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-mai-image-2.5", "object": "model", "created": 1677610602, "owned_by": "openai"}	31	2026-09-20 22:00:19.50967+00
2	openrouter-grok-imagine-image-quality	openrouter-grok-imagine-image-quality	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-grok-imagine-image-quality", "object": "model", "created": 1677610602, "owned_by": "openai"}	32	2026-09-20 22:00:19.50967+00
2	openai-tts-1	openai-tts-1	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openai-tts-1", "object": "model", "created": 1677610602, "owned_by": "openai"}	83	2026-09-20 22:00:19.509697+00
2	openrouter-recraft-v4-pro	openrouter-recraft-v4-pro	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-recraft-v4-pro", "object": "model", "created": 1677610602, "owned_by": "openai"}	33	2026-09-20 22:00:19.509671+00
2	openrouter-recraft-v4	openrouter-recraft-v4	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-recraft-v4", "object": "model", "created": 1677610602, "owned_by": "openai"}	34	2026-09-20 22:00:19.509671+00
2	openrouter-recraft-v3	openrouter-recraft-v3	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openrouter-recraft-v3", "object": "model", "created": 1677610602, "owned_by": "openai"}	35	2026-09-20 22:00:19.509672+00
2	openrouter-free-auto	openrouter-free-auto	DISCOVERED	t	200000	t	t	f	{}	f	\N	{"id": "openrouter-free-auto", "object": "model", "created": 1677610602, "owned_by": "openai"}	36	2026-09-20 22:00:19.509672+00
2	groq-whisper-large-v3	groq-whisper-large-v3	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-whisper-large-v3", "object": "model", "created": 1677610602, "owned_by": "openai"}	49	2026-09-20 22:00:19.509678+00
2	groq-whisper-large-v3-turbo	groq-whisper-large-v3-turbo	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-whisper-large-v3-turbo", "object": "model", "created": 1677610602, "owned_by": "openai"}	50	2026-09-20 22:00:19.509678+00
2	openrouter-bge-m3	openrouter-bge-m3	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-bge-m3", "object": "model", "created": 1677610602, "owned_by": "openai"}	51	2026-09-20 22:00:19.509679+00
2	local-kokoro-tts	local-kokoro-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "local-kokoro-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	52	2026-09-20 22:00:19.509679+00
2	groq-orpheus-english	groq-orpheus-english	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-orpheus-english", "object": "model", "created": 1677610602, "owned_by": "openai"}	53	2026-09-20 22:00:19.50968+00
2	groq-orpheus-arabic-saudi	groq-orpheus-arabic-saudi	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "groq-orpheus-arabic-saudi", "object": "model", "created": 1677610602, "owned_by": "openai"}	54	2026-09-20 22:00:19.50968+00
2	mistral-large	mistral-large	DISCOVERED	t	262144	t	t	f	{}	f	\N	{"id": "mistral-large", "object": "model", "created": 1677610602, "owned_by": "openai"}	55	2026-09-20 22:00:19.509681+00
2	cohere-rerank-v4.0-pro	cohere-rerank-v4.0-pro	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "cohere-rerank-v4.0-pro", "object": "model", "created": 1677610602, "owned_by": "openai"}	59	2026-09-20 22:00:19.509683+00
2	cohere-rerank-v4.0-fast	cohere-rerank-v4.0-fast	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "cohere-rerank-v4.0-fast", "object": "model", "created": 1677610602, "owned_by": "openai"}	60	2026-09-20 22:00:19.509684+00
2	openrouter-mai-transcribe-2	openrouter-mai-transcribe-2	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-mai-transcribe-2", "object": "model", "created": 1677610602, "owned_by": "openai"}	68	2026-09-20 22:00:19.509689+00
2	openrouter-whisper-large-v3-turbo	openrouter-whisper-large-v3-turbo	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-whisper-large-v3-turbo", "object": "model", "created": 1677610602, "owned_by": "openai"}	69	2026-09-20 22:00:19.509689+00
2	openrouter-fish-s2.1-pro-tts	openrouter-fish-s2.1-pro-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-fish-s2.1-pro-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	70	2026-09-20 22:00:19.50969+00
2	mistral-medium	mistral-medium	DISCOVERED	t	262144	t	t	f	{}	t	standard	{"id": "mistral-medium", "object": "model", "created": 1677610602, "owned_by": "openai"}	56	2026-09-20 22:00:19.509681+00
2	google-gemini-2.5-flash-image	google-gemini-2.5-flash-image	DISCOVERED	f	32768	t	t	t	{}	f	\N	{"id": "google-gemini-2.5-flash-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	75	2026-09-20 22:00:19.509692+00
2	ds-deepseek-v4.1-flash	ds-deepseek-v4.1-flash	DISCOVERED	t	1000000	t	t	f	{}	f	standard	{"id": "ds-deepseek-v4.1-flash", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1000000, "max_output_tokens": 384000}	61	2026-09-20 22:00:19.509685+00
2	google-gemini-3.1-flash-image	google-gemini-3.1-flash-image	DISCOVERED	f	65536	t	f	t	{}	t	standard	{"id": "google-gemini-3.1-flash-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	76	2026-09-20 22:00:19.509693+00
2	openrouter-mai-image-2.5-pro	openrouter-mai-image-2.5-pro	DISCOVERED	f	\N	f	f	t	{}	t	standard	{"id": "openrouter-mai-image-2.5-pro", "object": "model", "created": 1677610602, "owned_by": "openai"}	30	2026-09-20 22:00:19.509669+00
2	google-gemini-3.1-pro-preview	google-gemini-3.1-pro-preview	DISCOVERED	t	1048576	t	t	f	{}	f	premium	{"id": "google-gemini-3.1-pro-preview", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1048576, "max_output_tokens": 65536}	71	2026-09-20 22:00:19.50969+00
2	openai-gpt-image-2.5-flare	openai-gpt-image-2.5-flare	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openai-gpt-image-2.5-flare", "object": "model", "created": 1677610602, "owned_by": "openai"}	80	2026-09-20 22:00:19.509695+00
2	openai-gpt-4o-mini-tts	openai-gpt-4o-mini-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openai-gpt-4o-mini-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	82	2026-09-20 22:00:19.509696+00
2	openai-tts-1-hd	openai-tts-1-hd	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openai-tts-1-hd", "object": "model", "created": 1677610602, "owned_by": "openai"}	84	2026-09-20 22:00:19.509697+00
2	openrouter-grok-voice-tts-1.0	openrouter-grok-voice-tts-1.0	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-grok-voice-tts-1.0", "object": "model", "created": 1677610602, "owned_by": "openai"}	85	2026-09-20 22:00:19.509698+00
2	openrouter-fish-s2.1-pro-free-tts	openrouter-fish-s2.1-pro-free-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "openrouter-fish-s2.1-pro-free-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	86	2026-09-20 22:00:19.509698+00
2	google-gemini-2.5-flash-preview-tts	google-gemini-2.5-flash-preview-tts	DISCOVERED	f	\N	f	f	f	{}	f	\N	{"id": "google-gemini-2.5-flash-preview-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	87	2026-09-20 22:00:19.509699+00
2	google-gemini-2.5-pro-preview-tts	google-gemini-2.5-pro-preview-tts	DISCOVERED	f	1048576	t	t	f	{}	f	\N	{"id": "google-gemini-2.5-pro-preview-tts", "object": "model", "created": 1677610602, "owned_by": "openai"}	88	2026-09-20 22:00:19.509699+00
2	openai-gpt-image-1-mini	openai-gpt-image-1-mini	DISCOVERED	f	\N	f	f	t	{}	f	\N	{"id": "openai-gpt-image-1-mini", "object": "model", "created": 1677610602, "owned_by": "openai"}	161	2026-09-23 16:03:56.931554+00
2	openai-gpt-image-2	openai-gpt-image-2	DISCOVERED	f	\N	t	f	t	{}	t	standard	{"id": "openai-gpt-image-2", "object": "model", "created": 1677610602, "owned_by": "openai"}	160	2026-09-23 16:03:56.931553+00
2	openai-gpt-image-2.5-sunburst	openai-gpt-image-2.5-sunburst	DISCOVERED	f	\N	f	f	t	{}	f	standard	{"id": "openai-gpt-image-2.5-sunburst", "object": "model", "created": 1677610602, "owned_by": "openai"}	81	2026-09-20 22:00:19.509695+00
2	google-gemini-3.1-flash-lite-image	google-gemini-3.1-flash-lite-image	DISCOVERED	f	65536	t	t	t	{}	t	standard	{"id": "google-gemini-3.1-flash-lite-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	77	2026-09-20 22:00:19.509693+00
2	google-gemini-3-pro-image	google-gemini-3-pro-image	DISCOVERED	f	65536	t	f	t	{}	t	standard	{"id": "google-gemini-3-pro-image", "object": "model", "created": 1677610602, "owned_by": "openai"}	78	2026-09-20 22:00:19.509694+00
2	local-bge-m3	local-bge-m3	DISCOVERED	f	8192	f	f	f	{}	f	\N	{"id": "local-bge-m3", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 8192}	159	2026-09-23 16:03:56.931547+00
2	openai-gpt-5.6-luna	openai-gpt-5.6-luna	DISCOVERED	t	922000	t	t	f	{}	f	standard	{"id": "openai-gpt-5.6-luna", "object": "model", "created": 1677610602, "owned_by": "openai"}	79	2026-09-20 22:00:19.509694+00
1	google-gemini-3.7-flash	google-gemini-3.7-flash	DISCOVERED	t	1048576	t	t	f	{}	f	standard	{"id": "google-gemini-3.7-flash", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1048576, "max_output_tokens": 65536}	141	2026-09-23 16:03:56.840076+00
2	google-gemini-3.7-flash	google-gemini-3.7-flash	DISCOVERED	t	1048576	t	t	f	{}	t	standard	{"id": "google-gemini-3.7-flash", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1048576, "max_output_tokens": 65536}	72	2026-09-20 22:00:19.509691+00
1	google-gemini-3.8-flash	google-gemini-3.8-flash	DISCOVERED	t	1048576	t	t	f	{}	f	standard	{"id": "google-gemini-3.8-flash", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1048576, "max_output_tokens": 65536}	142	2026-09-23 16:03:56.840077+00
2	google-gemini-3.8-flash	google-gemini-3.8-flash	DISCOVERED	t	1048576	t	t	f	{}	t	standard	{"id": "google-gemini-3.8-flash", "object": "model", "created": 1677610602, "owned_by": "openai", "max_input_tokens": 1048576, "max_output_tokens": 65536}	73	2026-09-20 22:00:19.509691+00
\.


--
-- Data for Name: workspaces; Type: TABLE DATA; Schema: public; Owner: surfsense
--

COPY public.workspaces (name, description, citations_enabled, api_access_enabled, qna_custom_instructions, shared_memory_md, chat_model_id, image_gen_model_id, vision_model_id, llm_setup_completed_at, knowledge_store_enabled, last_indexed_revision, user_id, id, created_at) FROM stdin;
My Workspace	Your personal workspace	t	t			0	-2001	-4	2026-09-20 22:00:19.816281+00	f	\N	52fb3fe6-7da5-400b-8d36-5717af0b1b13	1	2026-09-20 19:25:43.658574+00
My Workspace	Your personal workspace	t	f			0	-2001	-4	\N	f	\N	0b7b3361-c6d7-4c0a-a8cf-c28f044ff9f3	3	2026-09-25 19:11:11.042523+00
My Workspace	Your personal workspace	t	f			0	-2001	-4	\N	f	\N	1808c83e-f8a9-484a-804c-b19424887377	6	2026-09-27 05:42:15.090559+00
My Workspace	Your personal workspace	t	f			0	-2001	-4	\N	f	\N	d3f65dcf-007a-4e6c-9654-60895cde97fd	7	2026-10-10 02:47:09.685432+00
My Workspace	Your personal workspace	t	f			0	-2001	-4	\N	f	\N	968e4479-135f-42fc-be2c-88b18c8a682b	9	2026-10-10 17:29:21.475286+00
\.


--
-- Name: models_id_seq; Type: SEQUENCE SET; Schema: public; Owner: surfsense
--

SELECT pg_catalog.setval('public.models_id_seq', 163, true);


--
-- Name: workspaces_id_seq; Type: SEQUENCE SET; Schema: public; Owner: surfsense
--

SELECT pg_catalog.setval('public.workspaces_id_seq', 10, true);


--
-- PostgreSQL database dump complete
--

\unrestrict vTNc7wEwMWgx42kJOlLBqKSjQcbdcx3fYtpYCy82y9mj4btFJiMzqMaf6TK17X5

