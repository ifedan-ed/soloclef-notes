"""Give a child interpreter the suite's embedding provider.

A few tests start a fresh interpreter to prove something about import cost
(``tests/unit/knowledge_store/index/test_queue.py``,
``tests/unit/knowledge_store/remote/test_queue.py``,
``tests/unit/notebooks/test_autoname.py``). The child inherits the
``EMBEDDING_MODEL`` that ``tests/conftest.py`` set, but not the chonkie
provider registration that goes with it -- that lives in this process's
memory, and ``app.config`` would refuse the name.

``tests/conftest.py`` puts this directory on the child's ``PYTHONPATH``, and
CPython imports ``sitecustomize`` while it starts up, before the ``-c`` or
``-m`` code runs. So the registration is there in time, and the tests say
nothing about embeddings.
"""

from tests import fake_embeddings

fake_embeddings.register()
