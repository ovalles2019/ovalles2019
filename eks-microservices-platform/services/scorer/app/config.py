"""Environment-driven configuration.

Mirrors internal/platform/config in the Go services: every value is validated at
import time and a bad value stops the process, so a misconfiguration becomes a
pod that never passes its startup probe rather than one that serves wrong
answers.
"""

from __future__ import annotations

import os
from dataclasses import dataclass


class ConfigError(ValueError):
    """Raised when the environment holds an unusable value."""


@dataclass(frozen=True)
class Settings:
    service_name: str
    version: str
    environment: str
    log_level: str
    port: int
    workers: int
    anomaly_threshold: float
    drain_delay_seconds: float
    expose_openapi: bool


def _int(key: str, default: int, *, minimum: int = 1, maximum: int | None = None) -> int:
    raw = os.getenv(key, "")
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ConfigError(f"config {key}: {raw!r} is not an integer") from exc
    if value < minimum:
        raise ConfigError(f"config {key}: must be at least {minimum}")
    if maximum is not None and value > maximum:
        raise ConfigError(f"config {key}: must be at most {maximum}")
    return value


def _float(key: str, default: float, *, minimum: float = 0.0) -> float:
    raw = os.getenv(key, "")
    if not raw:
        return default
    try:
        value = float(raw)
    except ValueError as exc:
        raise ConfigError(f"config {key}: {raw!r} is not a number") from exc
    if value < minimum:
        raise ConfigError(f"config {key}: must be at least {minimum}")
    return value


def _bool(key: str, default: bool) -> bool:
    raw = os.getenv(key, "").strip().lower()
    if not raw:
        return default
    if raw in ("1", "true", "yes", "on"):
        return True
    if raw in ("0", "false", "no", "off"):
        return False
    raise ConfigError(f"config {key}: {raw!r} is not a boolean")


def load_settings() -> Settings:
    return Settings(
        service_name=os.getenv("SERVICE_NAME", "scorer"),
        version=os.getenv("SERVICE_VERSION", "dev"),
        environment=os.getenv("ENVIRONMENT", "local"),
        log_level=os.getenv("LOG_LEVEL", "info"),
        port=_int("PORT", 8080, minimum=1, maximum=65535),
        # One worker per pod by default, so the HPA scales pods rather than
        # having each pod hide load behind its own process pool. Two layers of
        # concurrency make the CPU signal the HPA reads much harder to reason
        # about; see docs/adr/0011-one-worker-per-pod.md.
        workers=_int("WORKERS", 1, minimum=1, maximum=32),
        anomaly_threshold=_float("ANOMALY_THRESHOLD", 3.5, minimum=0.1),
        # Must stay below the pod's terminationGracePeriodSeconds.
        drain_delay_seconds=_float("DRAIN_DELAY_SECONDS", 5.0, minimum=0.0),
        expose_openapi=_bool("EXPOSE_OPENAPI", False),
    )
