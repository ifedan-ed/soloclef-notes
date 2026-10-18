import ssl

import httpx

#: LlamaCloud (app/etl_pipeline/parsers/llamacloud.py): a file is sent again
#: after an empty result or a job LlamaCloud ends FAILED or CANCELLED, up to
#: LLAMACLOUD_MAX_RETRIES times in all, waiting LLAMACLOUD_BASE_DELAY seconds
#: doubling to LLAMACLOUD_MAX_DELAY between them.
LLAMACLOUD_MAX_RETRIES = 5
LLAMACLOUD_BASE_DELAY = 10
LLAMACLOUD_MAX_DELAY = 120
#: Further tries the llama-cloud client gives each request itself after a
#: network error, a timeout, 408, 409, 429 or 5xx (waits of about 0.5, 1, 2,
#: 4 and 8 s, or the server's Retry-After up to 60 s).
LLAMACLOUD_REQUEST_RETRIES = 5
#: The v2 Parse API's ceiling on processing_control.timeouts.base_in_seconds.
LLAMACLOUD_MAX_BASE_JOB_TIMEOUT = 7200
#: The shortest time this server waits for a LlamaCloud job, in seconds.
LLAMACLOUD_MIN_JOB_WAIT = 2000
#: Polls of a LlamaCloud job: every 1 s, a second longer each time, at most 5 s.
LLAMACLOUD_POLL_INTERVAL = 1.0
LLAMACLOUD_MAX_POLL_INTERVAL = 5.0

#: Datalab: fewer attempts than LlamaCloud, since every resubmission that
#: reaches Datalab is billed again.
DATALAB_MAX_RETRIES = 3
DATALAB_BASE_DELAY = 10
DATALAB_MAX_DELAY = 120
DATALAB_POLL_INTERVAL = 2.0
DATALAB_RETRYABLE_EXCEPTIONS = (
    ssl.SSLError,
    httpx.ConnectError,
    httpx.ConnectTimeout,
    httpx.ReadError,
    httpx.ReadTimeout,
    httpx.WriteError,
    httpx.WriteTimeout,
    httpx.RemoteProtocolError,
    httpx.LocalProtocolError,
    ConnectionError,
    ConnectionResetError,
    TimeoutError,
    OSError,
)

UPLOAD_BYTES_PER_SECOND_SLOW = 100 * 1024
MIN_UPLOAD_TIMEOUT = 120
MAX_UPLOAD_TIMEOUT = 1800
BASE_JOB_TIMEOUT = 600
PER_PAGE_JOB_TIMEOUT = 60


def calculate_upload_timeout(file_size_bytes: int) -> float:
    estimated_time = (file_size_bytes / UPLOAD_BYTES_PER_SECOND_SLOW) * 1.5
    return max(MIN_UPLOAD_TIMEOUT, min(estimated_time, MAX_UPLOAD_TIMEOUT))


def calculate_job_timeout(estimated_pages: int, file_size_bytes: int) -> float:
    page_based_timeout = BASE_JOB_TIMEOUT + (estimated_pages * PER_PAGE_JOB_TIMEOUT)
    size_based_timeout = BASE_JOB_TIMEOUT + (file_size_bytes / (10 * 1024 * 1024)) * 60
    return max(page_based_timeout, size_based_timeout)
