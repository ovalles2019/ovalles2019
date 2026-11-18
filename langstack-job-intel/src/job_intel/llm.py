"""LangChain layer: one factory for the chat model every component shares."""

from functools import lru_cache

from langchain_anthropic import ChatAnthropic

from .config import settings


@lru_cache(maxsize=4)
def get_llm(effort: str | None = None) -> ChatAnthropic:
    # Opus 5.5: thinking is always adaptive and can't be disabled, so depth is
    # controlled only through effort (its default is "medium"; we set it explicitly).
    return ChatAnthropic(
        model=settings.model,
        max_tokens=16000,
        effort=effort or settings.effort,
    )
