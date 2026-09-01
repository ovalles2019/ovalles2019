# Multi-stage build for the Python scorer.
#
# Python cannot reach the `scratch` image the Go services use, so this aims for
# the next best thing: a distroless runtime with no shell and no package
# manager, and a virtualenv copied in from a builder that is discarded.

# --- Build stage -------------------------------------------------------------
FROM python:3.12-slim AS build

ENV PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /src

# A virtualenv rather than installing into the system interpreter, because the
# runtime stage copies this one directory and gets a complete, self-contained
# dependency tree with nothing else from the builder.
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Dependency metadata first, so a source edit does not invalidate the install
# layer and force a full reinstall on every build.
COPY services/scorer/pyproject.toml services/scorer/README.md* ./
COPY services/scorer/app ./app

RUN pip install --no-cache-dir .

# Compiling to bytecode here means the runtime never needs to write .pyc files,
# which matters because its root filesystem is read-only. Without this, every
# import pays interpretation cost on a cold start.
RUN python -m compileall -q /opt/venv/lib

# --- Runtime stage -----------------------------------------------------------
#
# Distroless has a Python runtime and its shared libraries and nothing else: no
# shell, no pip, no apt. Code execution in this container has no `sh` to spawn
# and no package manager to pull tools with.
FROM gcr.io/distroless/python3-debian12:nonroot

WORKDIR /app

COPY --from=build /opt/venv /opt/venv
COPY services/scorer/app /app/app

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONPATH="/opt/venv/lib/python3.12/site-packages:/app" \
    # Unbuffered output, or logs sit in a pipe buffer and a pod that is
    # OOMKilled loses exactly the lines that would explain why.
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

# The distroless `nonroot` tag is uid 65532. The scorer chart overrides
# runAsUser to match rather than assuming the platform default of 10001.
USER 65532:65532

EXPOSE 8080

# uvicorn is invoked directly rather than through a shell wrapper, so it is
# PID 1 and receives SIGTERM. The application installs its own SIGTERM handler
# to fail readiness and drain before uvicorn stops accepting.
ENTRYPOINT ["python", "-m", "uvicorn", "app.main:app", \
            "--host", "0.0.0.0", \
            "--port", "8080", \
            "--workers", "1", \
            "--log-config", "/dev/null", \
            "--no-access-log", \
            "--timeout-graceful-shutdown", "25"]
