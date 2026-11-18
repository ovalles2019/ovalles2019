"""LangSmith + Langfuse wiring.

The two tools overlap (both trace LangChain runs), so each gets a distinct job:
  * LangSmith  -> development: traces while you iterate, datasets, experiments (evals/).
  * Langfuse   -> production: per-user traces, cost/latency dashboards, feedback scores.

LangSmith needs no code: LANGSMITH_TRACING=true makes LangChain/LangGraph report
automatically. Langfuse is attached as a callback handler.
"""

from langchain_core.callbacks import BaseCallbackHandler

from .config import settings


def callbacks() -> list[BaseCallbackHandler]:
    if not settings.langfuse_enabled:
        return []
    from langfuse.langchain import CallbackHandler  # reads LANGFUSE_* env vars

    return [CallbackHandler()]


def run_config(thread_id: str, user_id: str | None = None) -> dict:
    """Config passed to every graph call: checkpointer thread + trace metadata
    (Langfuse maps langfuse_user_id / langfuse_session_id onto its trace)."""
    meta = {"langfuse_session_id": thread_id}
    if user_id:
        meta["langfuse_user_id"] = user_id
    return {
        "configurable": {"thread_id": thread_id},
        "callbacks": callbacks(),
        "metadata": meta,
        "tags": ["job-intel"],
        "run_name": "job-intel",
    }


def flush() -> None:
    if settings.langfuse_enabled:
        from langfuse import get_client

        get_client().flush()
