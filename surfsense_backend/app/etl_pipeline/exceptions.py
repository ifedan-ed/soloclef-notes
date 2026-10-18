class EtlParseError(Exception):
    """Raised when an ETL parser fails to produce content."""


class EtlServiceUnavailableError(Exception):
    """Raised when the chosen parser cannot run here: an unrecognised cloud
    service, a missing API key or a parser library that is not installed."""


class EtlUnsupportedFileError(Exception):
    """Raised when a file type cannot be parsed by any ETL pipeline."""
