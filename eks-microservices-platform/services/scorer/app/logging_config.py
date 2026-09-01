"""Structured JSON logging to stdout.

The Go services emit slog JSON; this produces the same field names so one Loki
query or one CloudWatch Logs Insights query works across all three services
instead of needing a per-language parser.
"""

from __future__ import annotations

import json
import logging
import sys
from contextvars import ContextVar

# Carries the correlation id through async call stacks without threading it
# through every function signature.
request_id_var: ContextVar[str] = ContextVar("request_id", default="")

# Attributes LogRecord always defines; anything else on a record was added by
# the caller via `extra=` and belongs in the JSON output.
_STANDARD_ATTRS = frozenset(logging.LogRecord("", 0, "", 0, "", None, None).__dict__.keys()) | {
    "message",
    "asctime",
    "taskName",
}


class JSONFormatter(logging.Formatter):
    def __init__(self, service: str, version: str, environment: str) -> None:
        super().__init__()
        self._base = {"service": service, "version": version, "environment": environment}

    def format(self, record: logging.LogRecord) -> str:
        payload = dict(self._base)
        payload.update(
            {
                "time": self.formatTime(record, "%Y-%m-%dT%H:%M:%S%z"),
                "level": record.levelname.lower(),
                "msg": record.getMessage(),
                "logger": record.name,
            }
        )

        request_id = request_id_var.get()
        if request_id:
            payload["request_id"] = request_id

        for key, value in record.__dict__.items():
            if key not in _STANDARD_ATTRS and not key.startswith("_"):
                payload[key] = value

        if record.exc_info:
            payload["error"] = self.formatException(record.exc_info)

        # default=str keeps a non-serialisable value in a log call from raising
        # inside the logger and taking down the request that was being logged.
        return json.dumps(payload, default=str)


def configure_logging(level: str, service: str, version: str, environment: str) -> None:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JSONFormatter(service, version, environment))

    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(getattr(logging, level.upper(), logging.INFO))

    # uvicorn installs its own handlers; clearing them stops every line being
    # emitted twice, once structured and once as plain text.
    for name in ("uvicorn", "uvicorn.error", "uvicorn.access"):
        logger = logging.getLogger(name)
        logger.handlers = []
        logger.propagate = True
