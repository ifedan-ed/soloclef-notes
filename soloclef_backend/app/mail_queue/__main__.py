"""``python -m app.mail_queue``: the mail_sender service."""

import asyncio
import logging

from .service import main

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s"
)
asyncio.run(main())
