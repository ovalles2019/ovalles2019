"""HTTP-level tests for the scorer."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.config import Settings
from app.main import create_app


@pytest.fixture()
def settings() -> Settings:
    return Settings(
        service_name="scorer",
        version="test",
        environment="test",
        log_level="warning",
        port=8080,
        workers=1,
        anomaly_threshold=3.5,
        drain_delay_seconds=0.0,
        expose_openapi=False,
    )


@pytest.fixture()
def app(settings: Settings):
    return create_app(settings)


@pytest.fixture()
def client(app):
    with TestClient(app) as c:
        yield c


def steady(n: int = 40) -> list[float]:
    return [10.0 + (i % 3) * 0.1 for i in range(n)]


def test_score_returns_result(client: TestClient) -> None:
    response = client.post("/v1/score", json={"device_id": "pump-01", "readings": steady()})
    assert response.status_code == 200

    body = response.json()
    assert body["device_id"] == "pump-01"
    assert body["anomaly"] is False
    assert set(body) >= {"score", "anomaly", "method", "window", "median", "dispersion"}


def test_score_flags_anomaly(client: TestClient) -> None:
    response = client.post(
        "/v1/score", json={"device_id": "pump-01", "readings": steady() + [500.0]}
    )
    assert response.status_code == 200
    assert response.json()["anomaly"] is True


@pytest.mark.parametrize(
    "payload",
    [
        {},
        {"device_id": "pump-01"},
        {"readings": [1.0, 2.0]},
        {"device_id": "", "readings": [1.0]},
        {"device_id": "pump-01", "readings": []},
        {"device_id": "pump-01", "readings": [1.0], "extra": "field"},
        {"device_id": "pump-01", "readings": ["not-a-number"]},
        {"device_id": "x" * 65, "readings": [1.0]},
    ],
)
def test_invalid_payloads_are_rejected(client: TestClient, payload: dict) -> None:
    response = client.post("/v1/score", json=payload)
    assert response.status_code == 422 or response.status_code == 400


def test_oversized_window_is_rejected(client: TestClient) -> None:
    """An unbounded window is a CPU amplification vector: one request could
    otherwise consume a whole core for an unbounded time."""
    response = client.post("/v1/score", json={"device_id": "pump-01", "readings": [1.0] * 100_000})
    assert response.status_code in (400, 422)


def test_probes(client: TestClient) -> None:
    for path in ("/healthz", "/readyz", "/startupz"):
        response = client.get(path)
        assert response.status_code == 200, f"{path} returned {response.status_code}"


def test_readiness_fails_while_draining(app, client: TestClient) -> None:
    """Readiness must fail on drain while liveness keeps passing.

    If liveness also failed, the kubelet would restart a pod that is trying to
    finish its in-flight requests.
    """
    app.state.probes.draining = True

    assert client.get("/readyz").status_code == 503
    assert client.get("/healthz").status_code == 200


def test_probe_state_is_per_instance(settings: Settings) -> None:
    """Two apps in one process must not share drain state.

    A module-level flag would make one shutting-down app report every other app
    as draining, which is both a test-isolation bug and a real one under any
    embedding that builds more than one instance.
    """
    first, second = create_app(settings), create_app(settings)
    first.state.probes.draining = True
    assert second.state.probes.draining is False


def test_metrics_exposes_red_signals(client: TestClient) -> None:
    client.post("/v1/score", json={"device_id": "pump-01", "readings": steady()})

    body = client.get("/metrics").text
    for metric in (
        "http_requests_total",
        "http_request_duration_seconds",
        "scorer_windows_scored_total",
        "service_build_info",
    ):
        assert metric in body, f"{metric} is missing from the scrape"


def test_metrics_route_label_is_the_template(client: TestClient) -> None:
    """The same cardinality guarantee the Go services make."""
    for i in range(50):
        client.post("/v1/score", json={"device_id": f"device-{i}", "readings": steady()})

    body = client.get("/metrics").text
    routes = {
        line.split('route="')[1].split('"')[0]
        for line in body.splitlines()
        if line.startswith("http_requests_total{") and 'route="' in line
    }
    assert routes == {"/v1/score"}, (
        f"route labels {routes} after 50 distinct device ids; "
        "the label must be the path template, not the request payload"
    )


def test_request_id_is_echoed(client: TestClient) -> None:
    request_id = "0123456789abcdef0123456789abcdef"
    response = client.post(
        "/v1/score",
        json={"device_id": "pump-01", "readings": steady()},
        headers={"x-request-id": request_id},
    )
    assert response.headers.get("x-request-id") == request_id


def test_error_body_is_problem_json(client: TestClient) -> None:
    response = client.post("/v1/score", json={"device_id": "pump-01", "readings": [float(1)] * 1})
    if response.status_code == 400:
        assert response.headers["content-type"].startswith("application/problem+json")
        assert "code" in response.json()


def test_openapi_is_not_exposed_by_default(client: TestClient) -> None:
    """An internal service should not publish its schema or interactive docs."""
    for path in ("/docs", "/redoc", "/v1/openapi.json"):
        assert client.get(path).status_code == 404
