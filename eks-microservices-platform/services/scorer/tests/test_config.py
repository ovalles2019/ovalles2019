"""Configuration must fail loudly at boot.

A service that silently falls back to a default on a bad value serves wrong
answers with a green readiness probe. Raising instead turns the same mistake
into a pod that never passes its startup probe, so a bad rollout stalls rather
than replacing healthy pods.
"""

from __future__ import annotations

import pytest

from app.config import ConfigError, load_settings


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch):
    for key in (
        "SERVICE_NAME",
        "SERVICE_VERSION",
        "ENVIRONMENT",
        "LOG_LEVEL",
        "PORT",
        "WORKERS",
        "ANOMALY_THRESHOLD",
        "DRAIN_DELAY_SECONDS",
        "EXPOSE_OPENAPI",
    ):
        monkeypatch.delenv(key, raising=False)


def test_defaults_are_usable_with_no_environment() -> None:
    settings = load_settings()
    assert settings.service_name == "scorer"
    assert settings.port == 8080
    assert settings.workers == 1
    assert settings.expose_openapi is False


def test_values_are_read_from_the_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PORT", "9000")
    monkeypatch.setenv("WORKERS", "4")
    monkeypatch.setenv("ANOMALY_THRESHOLD", "2.5")
    monkeypatch.setenv("EXPOSE_OPENAPI", "true")

    settings = load_settings()
    assert (settings.port, settings.workers, settings.anomaly_threshold) == (9000, 4, 2.5)
    assert settings.expose_openapi is True


@pytest.mark.parametrize(
    ("key", "value"),
    [
        ("PORT", "not-a-number"),
        ("PORT", "0"),
        ("PORT", "70000"),
        ("WORKERS", "0"),
        ("WORKERS", "-1"),
        ("WORKERS", "1000"),
        ("ANOMALY_THRESHOLD", "abc"),
        ("ANOMALY_THRESHOLD", "0"),
        ("DRAIN_DELAY_SECONDS", "-1"),
        ("EXPOSE_OPENAPI", "maybe"),
    ],
)
def test_bad_values_raise(monkeypatch: pytest.MonkeyPatch, key: str, value: str) -> None:
    monkeypatch.setenv(key, value)
    with pytest.raises(ConfigError) as excinfo:
        load_settings()
    assert key in str(excinfo.value), "the error must name the offending variable"


@pytest.mark.parametrize("value", ["1", "true", "yes", "on", "TRUE"])
def test_truthy_boolean_spellings(monkeypatch: pytest.MonkeyPatch, value: str) -> None:
    monkeypatch.setenv("EXPOSE_OPENAPI", value)
    assert load_settings().expose_openapi is True


@pytest.mark.parametrize("value", ["0", "false", "no", "off", "FALSE"])
def test_falsy_boolean_spellings(monkeypatch: pytest.MonkeyPatch, value: str) -> None:
    monkeypatch.setenv("EXPOSE_OPENAPI", value)
    assert load_settings().expose_openapi is False


def test_empty_value_falls_back_to_default(monkeypatch: pytest.MonkeyPatch) -> None:
    # An unset variable in a Kubernetes manifest often arrives as an empty
    # string rather than being absent; that must mean "default", not "invalid".
    monkeypatch.setenv("PORT", "")
    assert load_settings().port == 8080
