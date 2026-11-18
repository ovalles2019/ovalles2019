"""Central settings, read once from the environment (.env supported)."""

import os
from dataclasses import dataclass

from dotenv import load_dotenv

load_dotenv()


@dataclass(frozen=True)
class Settings:
    model: str = os.getenv("JOB_INTEL_MODEL", "claude-opus-5-5")
    effort: str = os.getenv("JOB_INTEL_EFFORT", "medium")
    fit_threshold: int = int(os.getenv("JOB_INTEL_FIT_THRESHOLD", "60"))
    langfuse_enabled: bool = bool(
        os.getenv("LANGFUSE_PUBLIC_KEY") and os.getenv("LANGFUSE_SECRET_KEY")
    )
    langsmith_enabled: bool = os.getenv("LANGSMITH_TRACING", "").lower() == "true"


settings = Settings()
