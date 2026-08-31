"""FastAPI application for the scorer service.

The scorer is deliberately stateless and CPU-bound: it is the HPA target, and
the only service in the platform whose replica count should track load rather
than availability requirements.
"""

from __future__ import annotations

import contextlib
import logging
import signal
import time
from contextlib import asynccontextmanager

from fastapi import APIRouter, FastAPI, Request, Response
from fastapi.responses import JSONResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest
from pydantic import BaseModel, Field, field_validator

from .config import Settings, load_settings
from .logging_config import configure_logging, request_id_var
from .metrics import ScorerMetrics, build_metrics
from .scoring import MAX_WINDOW, ScoringError, score_window

log = logging.getLogger("scorer")


class ScoreRequest(BaseModel):
    """A window of readings to score."""

    device_id: str = Field(min_length=1, max_length=64)
    readings: list[float] = Field(min_length=1, max_length=MAX_WINDOW)

    # Rejecting unknown fields means a client typo fails loudly rather than
    # being silently dropped and producing a puzzling result.
    model_config = {"extra": "forbid"}

    @field_validator("readings")
    @classmethod
    def _finite(cls, values: list[float]) -> list[float]:
        for value in values:
            if value != value or value in (float("inf"), float("-inf")):
                raise ValueError("readings must all be finite numbers")
        return values


class ProbeState:
    """Boot and drain state for one application instance.

    Held on ``app.state`` rather than in a module global so two apps in one
    process (which is exactly what a test suite is) cannot see each other's
    drain flag.
    """

    def __init__(self) -> None:
        self.started = False
        self.draining = False


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings: Settings = app.state.settings
    state: ProbeState = app.state.probes

    # SIGTERM flips readiness to false, waits for the endpoints controller to
    # propagate the NotReady state, then defers to uvicorn's own handler.
    # Without this the pod keeps passing readiness while it shuts down and the
    # data plane keeps routing requests it can no longer serve.
    previous = signal.getsignal(signal.SIGTERM)

    def _drain(signum, frame):  # pragma: no cover - exercised in the container
        state.draining = True
        log.info("sigterm received, failing readiness before shutdown")
        time.sleep(settings.drain_delay_seconds)
        if callable(previous):
            previous(signum, frame)

    # signal() only works on the main thread; under a test client it does not,
    # and running without the drain hook is correct there.
    with contextlib.suppress(ValueError):
        signal.signal(signal.SIGTERM, _drain)

    state.started = True
    log.info(
        "scorer starting",
        extra={"version": settings.version, "environment": settings.environment},
    )
    yield
    state.draining = True


def create_app(settings: Settings | None = None) -> FastAPI:
    """Build the application. Injectable settings keep tests hermetic."""
    settings = settings or load_settings()
    configure_logging(
        settings.log_level, settings.service_name, settings.version, settings.environment
    )

    app = FastAPI(
        title="scorer",
        version=settings.version,
        lifespan=lifespan,
        # The public surface is one endpoint; interactive docs are an
        # unnecessary attack surface on an internal service.
        docs_url=None,
        redoc_url=None,
        openapi_url="/v1/openapi.json" if settings.expose_openapi else None,
    )
    app.state.settings = settings
    app.state.probes = ProbeState()
    app.state.metrics = build_metrics(settings.service_name, settings.version, settings.environment)

    app.middleware("http")(_observability_middleware)
    app.include_router(_api_router())
    app.include_router(_admin_router())

    @app.exception_handler(ScoringError)
    async def _scoring_error(request: Request, exc: ScoringError) -> JSONResponse:
        return _problem(request, 400, "invalid_request", str(exc))

    return app


async def _observability_middleware(request: Request, call_next):
    """Record RED metrics and propagate the correlation id."""
    metrics: ScorerMetrics = request.app.state.metrics

    request_id = request.headers.get("x-request-id", "")
    token = request_id_var.set(request_id)

    start = time.perf_counter()
    metrics.in_flight.inc()
    try:
        response = await call_next(request)
    finally:
        metrics.in_flight.dec()
        request_id_var.reset(token)

    # The matched route template, not the raw path.
    route = request.scope.get("route")
    route_label = getattr(route, "path", "unmatched")
    status = response.status_code

    metrics.requests.labels(request.method, route_label, str(status), f"{status // 100}xx").inc()
    metrics.latency.labels(request.method, route_label).observe(time.perf_counter() - start)

    if request_id:
        response.headers["x-request-id"] = request_id
    return response


def _api_router() -> APIRouter:
    router = APIRouter()

    @router.post("/v1/score")
    async def score(payload: ScoreRequest, request: Request) -> JSONResponse:
        settings: Settings = request.app.state.settings
        metrics: ScorerMetrics = request.app.state.metrics

        metrics.window_size.observe(len(payload.readings))
        try:
            result = score_window(payload.readings, threshold=settings.anomaly_threshold)
        except ScoringError as exc:
            metrics.scored.labels("invalid").inc()
            return _problem(request, 400, "invalid_request", str(exc))

        metrics.scored.labels("anomaly" if result.anomaly else "normal").inc()
        body = result.as_dict()
        body["device_id"] = payload.device_id
        return JSONResponse(body)

    return router


def _admin_router() -> APIRouter:
    """Probes and the scrape target."""
    router = APIRouter(include_in_schema=False)

    @router.get("/healthz")
    async def healthz(request: Request) -> JSONResponse:
        # Liveness ignores drain state and has no dependency checks: a draining
        # pod is alive, and restarting it would abandon in-flight requests.
        settings: Settings = request.app.state.settings
        return JSONResponse(
            {"status": "ok", "service": settings.service_name, "version": settings.version}
        )

    @router.get("/startupz")
    async def startupz(request: Request) -> JSONResponse:
        state: ProbeState = request.app.state.probes
        if not state.started:
            return JSONResponse({"status": "starting"}, status_code=503)
        return JSONResponse({"status": "ok"})

    @router.get("/readyz")
    async def readyz(request: Request) -> JSONResponse:
        settings: Settings = request.app.state.settings
        state: ProbeState = request.app.state.probes

        if not state.started:
            return JSONResponse({"status": "starting"}, status_code=503)
        if state.draining:
            return JSONResponse({"status": "draining"}, status_code=503)
        # The scorer is stateless with no dependencies, so being up is being
        # ready. Inventing a dependency check here would only add a failure mode.
        return JSONResponse(
            {"status": "ok", "service": settings.service_name, "version": settings.version}
        )

    @router.get("/metrics")
    async def metrics(request: Request) -> Response:
        registry = request.app.state.metrics.registry
        return Response(generate_latest(registry), media_type=CONTENT_TYPE_LATEST)

    return router


def _problem(request: Request, status: int, code: str, detail: str) -> JSONResponse:
    """RFC 7807 error body, matching the Go services' shape."""
    return JSONResponse(
        {
            "type": "about:blank",
            "title": "Bad Request" if status == 400 else "Error",
            "status": status,
            "detail": detail,
            "code": code,
            "request_id": request.headers.get("x-request-id", ""),
        },
        status_code=status,
        media_type="application/problem+json",
    )


# Module-level app for `uvicorn app.main:app`.
app = create_app()
