# The parser service image: Python, pymupdf4llm at the locked version, and the
# few files of parse_service. Nothing from the application is copied in.
#
#   docker build -t soloclef-parse-service:latest soloclef_parse_service

FROM python:3.12-slim-bookworm AS dependencies
# uv only resolves here; it is not in the final image.
COPY --from=ghcr.io/astral-sh/uv:0.11.9 /uv /usr/local/bin/uv
WORKDIR /app
COPY pyproject.toml uv.lock ./
# --frozen: the lockfile decides every version, so the image cannot pick up a
# newer parser by being rebuilt. --no-dev leaves pytest out.
RUN uv sync --frozen --no-dev --no-install-project --compile-bytecode

FROM python:3.12-slim-bookworm
# Unprivileged: the service opens files strangers uploaded, in a C library.
RUN useradd --system --uid 10001 --home-dir /app parse
WORKDIR /app
COPY --from=dependencies /app/.venv /app/.venv
COPY parse_service ./parse_service
ENV PATH=/app/.venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1
USER parse
EXPOSE 8080
# No settings are baked in: each PARSE_SERVICE_* variable is required and the
# process stops at start naming the one that is missing (parse_service/settings.py).
CMD ["python", "-m", "parse_service"]
