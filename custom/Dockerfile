# Soloclef backend as deployed here.
#
# This file used to apply build-time patches to upstream's code (embedding
# truncation headroom, and four fixes to structured-output handling in the
# video still review). They now live in ../soloclef_backend itself, with
# tests, so nothing is patched here any more; see README.md for what each one
# became. The image is kept as its own stage so the compose file and the
# build commands do not change, and so a future patch has an obvious home.
#
# The base is OUR source build, not the published image. Build it first,
# from the same checkout:
#   docker build -t soloclef-backend-src:latest --target production ../soloclef_backend
# then build this. Pointing BACKEND_BASE at the published upstream image
# would drop every local fix, because they are in the source, not in here.
ARG BACKEND_BASE=soloclef-backend-src:latest
FROM ${BACKEND_BASE}

# What sizes a chunk, baked so no start needs the network.
#
# `EMBEDDING_TOKENIZER` names the tokenizer that counts a chunk's size
# (app/config/chunk_sizing.py), and chonkie loads one from a directory holding
# tokenizer.json offline. This server embeds with BGE-M3, so its tokenizer is
# what has to be here: 17 MB against the image's 8.5 GB, and the alternative is
# a HuggingFace fetch on the start path of every cold container.
#
# Named here rather than in the source Dockerfile because the model is this
# deployment's choice, not the image's. A build that wants no tokenizer passes
# an empty CHUNK_TOKENIZER; a named one that cannot be downloaded fails the
# build rather than shipping an image whose server will not start.
ARG CHUNK_TOKENIZER=BAAI/bge-m3
ARG CHUNK_TOKENIZER_DIR=/opt/chunk-tokenizer
RUN if [ -n "$CHUNK_TOKENIZER" ]; then \
      python -c "import sys; from huggingface_hub import snapshot_download; \
snapshot_download(sys.argv[1], local_dir=sys.argv[2], \
allow_patterns=['tokenizer.json', 'tokenizer_config.json', 'special_tokens_map.json'])" \
        "$CHUNK_TOKENIZER" "$CHUNK_TOKENIZER_DIR" && \
      test -s "$CHUNK_TOKENIZER_DIR/tokenizer.json"; \
    fi
