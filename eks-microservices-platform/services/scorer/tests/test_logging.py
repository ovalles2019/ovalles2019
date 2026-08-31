"""The log format is a cross-service contract.

All three services emit the same JSON field names so one Loki or CloudWatch Logs
Insights query works across the whole platform instead of needing a per-language
parser. These tests pin that contract.
"""

from __future__ import annotations

import json
import logging

from app.logging_config import JSONFormatter, request_id_var


def format_record(**extra) -> dict:
    formatter = JSONFormatter("scorer", "1.2.3", "test")
    record = logging.LogRecord(
        name="scorer",
        level=logging.INFO,
        pathname=__file__,
        lineno=1,
        msg="scored window",
        args=None,
        exc_info=None,
    )
    for key, value in extra.items():
        setattr(record, key, value)
    return json.loads(formatter.format(record))


def test_emits_json_with_the_shared_field_names() -> None:
    payload = format_record()
    assert payload["msg"] == "scored window"
    assert payload["level"] == "info"
    assert payload["service"] == "scorer"
    assert payload["version"] == "1.2.3"
    assert payload["environment"] == "test"
    assert "time" in payload


def test_extra_fields_are_promoted_to_top_level_keys() -> None:
    # Structured fields must be queryable, not buried inside the message string.
    payload = format_record(device_id="pump-01", window=512)
    assert payload["device_id"] == "pump-01"
    assert payload["window"] == 512


def test_request_id_is_included_when_set() -> None:
    token = request_id_var.set("0123456789abcdef0123456789abcdef")
    try:
        assert format_record()["request_id"] == "0123456789abcdef0123456789abcdef"
    finally:
        request_id_var.reset(token)


def test_request_id_is_omitted_when_unset() -> None:
    assert "request_id" not in format_record()


def test_unserialisable_values_do_not_break_logging() -> None:
    """A log call must never be the thing that fails the request it describes."""

    class Opaque:
        def __repr__(self) -> str:
            return "<opaque>"

    payload = format_record(thing=Opaque())
    assert payload["thing"] == "<opaque>"


def test_exceptions_are_captured() -> None:
    formatter = JSONFormatter("scorer", "1.2.3", "test")
    try:
        raise ValueError("boom")
    except ValueError:
        import sys

        record = logging.LogRecord(
            "scorer", logging.ERROR, __file__, 1, "failed", None, sys.exc_info()
        )
        payload = json.loads(formatter.format(record))

    assert "error" in payload
    assert "ValueError: boom" in payload["error"]
