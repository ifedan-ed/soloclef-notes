"""Where an authored scene module sits in the Remotion harness, and so how it
imports the baked stagger helper.

The sandbox harness (``docker/sandbox/remotion``) writes scene ``N`` verbatim to
``src/scenes/scene-N.tsx`` and bundles it with esbuild, and the helper is
``stagger.ts`` at the harness root. A model told only "use the baked stagger
helper" guesses the path, and one wrong guess fails every scene in preflight,
so the prompt states the exact line, derived here from the same layout.
"""

from __future__ import annotations

import posixpath

# Mirrors render-utils.mjs ``writeSceneModules`` and the harness root layout.
SCENE_MODULE_DIR = "src/scenes"
STAGGER_MODULE = "stagger"

STAGGER_IMPORT_PATH = posixpath.relpath(STAGGER_MODULE, SCENE_MODULE_DIR)
STAGGER_IMPORT = f'import {{stagger}} from "{STAGGER_IMPORT_PATH}";'
