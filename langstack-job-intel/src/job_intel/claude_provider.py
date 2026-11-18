"""LangExtract provider plugin that routes inference through LangChain's ChatAnthropic.

LangExtract ships Gemini / OpenAI / Ollama providers. Registering this class lets
`lx.extract(model_id="claude-...")` work, and because the call goes through
LangChain, every extraction request is traced by LangSmith and Langfuse too.
"""

from collections.abc import Iterator, Sequence
from typing import Any

from langchain_core.callbacks import BaseCallbackHandler
from langextract.core import base_model, types
from langextract.providers import router

from .llm import get_llm


@router.register(r"^claude", priority=10)
class ClaudeLanguageModel(base_model.BaseLanguageModel):
    def __init__(
        self,
        model_id: str = "claude-opus-5-5",
        effort: str = "low",
        callbacks: list[BaseCallbackHandler] | None = None,
        **kwargs: Any,
    ):
        super().__init__(**kwargs)
        self.model_id = model_id
        # Extraction is pattern-matching against few-shot examples; low effort is enough.
        self._llm = get_llm(effort)
        self._callbacks = callbacks or []

    def infer(
        self, batch_prompts: Sequence[str], **kwargs: Any
    ) -> Iterator[Sequence[types.ScoredOutput]]:
        config = {"callbacks": self._callbacks, "run_name": "langextract", "tags": ["langextract"]}
        for prompt in batch_prompts:
            msg = self._llm.invoke(prompt, config=config)
            yield [types.ScoredOutput(score=1.0, output=msg.text)]
