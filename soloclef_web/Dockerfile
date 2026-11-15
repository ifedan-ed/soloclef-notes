# syntax=docker.io/docker/dockerfile:1

FROM node:20-alpine AS base

# Install dependencies only when needed
FROM base AS deps
# Check https://github.com/nodejs/docker-node/tree/b4117f9333da4138b03a546ec926ef50a31506c3#nodealpine to understand why libc6-compat might be needed.
RUN apk add --no-cache libc6-compat
WORKDIR /app

# Install pnpm
RUN corepack enable pnpm

# Copy package files
COPY package.json pnpm-lock.yaml* pnpm-workspace.yaml* .npmrc* ./

# First copy the config file and content to avoid fumadocs-mdx postinstall error
COPY source.config.ts ./
COPY content ./content

# Install dependencies with frozen lockfile
RUN pnpm i --frozen-lockfile


# Build the static site.
FROM base AS builder
WORKDIR /app

# git is required by the fumadocs-mdx lastModified plugin during `next build`.
# The .git directory is dockerignored, so git commands exit non-zero and the
# plugin falls back gracefully — but the binary itself must exist to avoid
# "spawn git ENOENT" failures.
RUN apk add --no-cache git

# Enable pnpm
RUN corepack enable pnpm

COPY --from=deps /app/node_modules ./node_modules
COPY . .

# Next.js collects completely anonymous telemetry data about general usage.
# Learn more here: https://nextjs.org/telemetry
# Uncomment the following line in case you want to disable telemetry during the build.
# ENV NEXT_TELEMETRY_DISABLED=1

# The commit this bundle is built from, reported with browser crash reports
# (lib/client-error-report.ts). .git is dockerignored, so pass it in:
#   docker build --build-arg NEXT_PUBLIC_BUILD_COMMIT=$(git rev-parse --short HEAD) ...
# Unset, reports carry the package.json version instead.
ARG NEXT_PUBLIC_BUILD_COMMIT=""
ENV NEXT_PUBLIC_BUILD_COMMIT=${NEXT_PUBLIC_BUILD_COMMIT}

# This server's own settings that the public pages print. Those pages are
# drawn here, at build time (the sitemap, robots.txt, canonical and share-card
# links, the JSON-LD contact point, the docs' sign-in wording), so the values
# come in as build args: docker-compose.go.yml fills them from docker/.env.go
# (SOLOCLEF_API_PUBLIC_URL, SOLOCLEF_API_SUPPORT_EMAIL, the OIDC and sign-up
# settings). None
# is a secret. Empty means the server has not published one: no absolute
# links, no contact address, no single sign-on in the docs (lib/brand,
# lib/sso-settings), and Request access instead of Create an account
# (lib/signups). Rebuild after changing any of them: Next.js writes every
# NEXT_PUBLIC_ value into the pages and scripts here, and the site is static
# files after that. (The contact page also asks the API for its address in
# the browser, so an address set later shows without a rebuild.) A plain `docker build` without them builds that
# unconfigured site, and the build log says so (next.config.ts).
ARG NEXT_PUBLIC_SITE_URL=""
ARG NEXT_PUBLIC_SUPPORT_EMAIL=""
ARG NEXT_PUBLIC_OIDC_ENABLED=""
ARG NEXT_PUBLIC_OIDC_BUTTON_LABEL=""
ARG NEXT_PUBLIC_OIDC_DISCOVERY_URL=""
ARG NEXT_PUBLIC_OIDC_AUTOCREATE_DOMAINS=""
ARG NEXT_PUBLIC_OIDC_ALLOW_SIGNUP=""
ARG NEXT_PUBLIC_SIGNUPS_OPEN=""
ENV NEXT_PUBLIC_SITE_URL="${NEXT_PUBLIC_SITE_URL}" \
	NEXT_PUBLIC_SUPPORT_EMAIL="${NEXT_PUBLIC_SUPPORT_EMAIL}" \
	NEXT_PUBLIC_OIDC_ENABLED="${NEXT_PUBLIC_OIDC_ENABLED}" \
	NEXT_PUBLIC_OIDC_BUTTON_LABEL="${NEXT_PUBLIC_OIDC_BUTTON_LABEL}" \
	NEXT_PUBLIC_OIDC_DISCOVERY_URL="${NEXT_PUBLIC_OIDC_DISCOVERY_URL}" \
	NEXT_PUBLIC_OIDC_AUTOCREATE_DOMAINS="${NEXT_PUBLIC_OIDC_AUTOCREATE_DOMAINS}" \
	NEXT_PUBLIC_OIDC_ALLOW_SIGNUP="${NEXT_PUBLIC_OIDC_ALLOW_SIGNUP}" \
	NEXT_PUBLIC_SIGNUPS_OPEN="${NEXT_PUBLIC_SIGNUPS_OPEN}"

# `next build` writes the static site to out/ (next.config.ts). Then the
# public link page gets its link-preview copy (scripts/share-card-template.ts
# says why), and every text file a compressed copy for Caddy to send as it is
# (scripts/precompress.ts). Node is used here, at build time, and nowhere after.
#
# NEXT_PUBLIC_BUILD_ID names this one build, new each time it runs, so a
# page can tell one deploy from the next (lib/chunk-reload/reload-guard.ts:
# one reload per build when its code fails to load). Made here rather than
# passed in: it is not a setting, and a cached layer is the same build.
RUN NEXT_PUBLIC_BUILD_ID="$(cat /proc/sys/kernel/random/uuid)" pnpm run build \
	&& pnpm exec tsx scripts/share-card-template.ts out \
	&& pnpm exec tsx scripts/precompress.ts out

# The site: the static files, served by the stack's Caddy, which is this
# image. docker-compose.go.yml builds its `proxy` service from this stage and
# mounts docker/proxy-go/Caddyfile and routes.caddy, which say how each
# address is answered (the pages, their cache headers and security headers,
# the API's paths). There is no Node.js in it: nothing on the server runs the
# web app, the browser does.
FROM caddy:2-alpine AS site
COPY --from=builder /app/out /srv
# Earlier builds' hashed files, for tabs opened before a deploy
# (site-image/kept-assets.sh says how). At start this build's files are
# added to the site_assets volume, and then, at once and every hour after,
# the builds that stopped being live more than 14 days ago (and the oldest
# past the script's MAX_BUILDS) are dropped. That runs beside Caddy, not
# before it, so the site is up as soon as Caddy is: this build serves its
# own files from /srv and never waits for the copy. A failure is in the log
# and costs old tabs a reload, nothing more, so it does not stop the site.
COPY site-image/kept-assets.sh /usr/local/bin/kept-assets
CMD ["/bin/sh", "-c", "kept-assets run 14 3600 & exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile"]
