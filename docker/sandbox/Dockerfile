FROM opensandbox/code-interpreter:v1.1.0

SHELL ["/bin/bash", "-c"]

USER root

# fonts-crosextra-carlito and -caladea are the open, metric-compatible twins
# of Calibri and Cambria. A slide deck is drawn by the API in those two
# families, its text sized by measuring Carlito and Caladea
# (soloclef_studio/deckrender); LibreOffice here substitutes them by name
# when it draws the deck's preview, so the preview, and the layout rules
# read from it, see exactly the line breaks the renderer measured. Without
# them LibreOffice falls back to a wider face and every box overflows.
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        fonts-crosextra-caladea \
        fonts-crosextra-carlito \
        fonts-dejavu-core \
        fonts-liberation \
        fonts-noto-cjk \
        libpango-1.0-0 \
        libpangoft2-1.0-0 \
        libreoffice \
        pandoc \
        poppler-utils \
    && rm -rf /var/lib/apt/lists/*

# The entrypoint activates the interpreter named by PYTHON_VERSION and binds the
# Jupyter kernel to it. The base default is 3.14, which lacks wheels for parts of
# the stack below; pin 3.12 so the kernel is the interpreter carrying them.
ENV PYTHON_VERSION=3.12

# code-interpreter-env.sh only mutates the calling shell, so it must be sourced
# in the same layer as the install. Unqualified pip would otherwise land in the
# unrelated Ubuntu /usr/bin/python3.12 that the kernel never sees.
RUN set -euo pipefail \
    && . /opt/code-interpreter/code-interpreter-env.sh python "${PYTHON_VERSION}" \
    && python3 -m pip install --no-cache-dir --break-system-packages \
        matplotlib \
        openpyxl \
        pandas \
        pypdf \
        python-docx \
        reportlab \
        weasyprint \
        xlsxwriter \
    && python3 -c "import sys; assert sys.version_info[:2] == (3, 12), sys.version; assert sys.prefix.startswith('/opt/python/versions'), sys.prefix" \
    && python3 -c "import docx, matplotlib, openpyxl, pandas, pypdf, reportlab, weasyprint, xlsxwriter"

# A global npm install is not on node's resolution path from /workspace, so pin
# the prefix and point NODE_PATH at it to make require('docx') work anywhere.
ENV NPM_CONFIG_PREFIX=/opt/node-global \
    NODE_PATH=/opt/node-global/lib/node_modules

RUN set -euo pipefail \
    && npm install --global docx \
    && node -e "require('docx')"

# Every family a drawn deck names is installed above, so fontconfig's
# cache is built once here rather than by the first LibreOffice run in each
# sandbox; the check stops the build if one is missing.
RUN set -euo pipefail \
    && fc-cache -f \
    && for family in Carlito Caladea; do \
        fc-list : family | grep -Fqi "${family}"; \
    done

# The base image also ships skills. Replace the formats Soloclef owns in one
# layer so OverlayFS records removed base-image files as deletions.
COPY skills /tmp/soloclef-skills
RUN set -euo pipefail \
    && mkdir -p /opt/skills \
    && for skill in /tmp/soloclef-skills/*; do \
        test -f "${skill}/SKILL.md" || continue; \
        name="${skill##*/}"; \
        rm -rf "/opt/skills/${name}"; \
        cp -a "${skill}" "/opt/skills/${name}"; \
        rm -rf "/opt/skills/${name}/scripts"; \
        cat /tmp/soloclef-skills/frontend-design/DESIGN.md >> "/opt/skills/${name}/SKILL.md"; \
    done \
    && rm -rf /tmp/soloclef-skills

ENTRYPOINT ["/opt/code-interpreter/code-interpreter.sh"]
