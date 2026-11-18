"""LangExtract layer: unstructured job posting -> source-grounded structured facts."""

import textwrap
from pathlib import Path
from typing import Any

import langextract as lx

from .claude_provider import ClaudeLanguageModel
from .config import settings

PROMPT = textwrap.dedent("""\
    Extract hiring facts from the job posting, in order of appearance.
    Use the exact text from the posting for each extraction; do not paraphrase.
    Classes: skill, seniority, compensation, location, work_model, employment_type.
    For each skill, set attribute "priority" to "required" or "preferred".""")

EXAMPLES = [
    lx.data.ExampleData(
        text=(
            "Senior Data Engineer (Remote, US). $150k-$180k. Full-time. "
            "Must have Python and Airflow; Kafka experience is a plus."
        ),
        extractions=[
            lx.data.Extraction("seniority", "Senior"),
            lx.data.Extraction("work_model", "Remote"),
            lx.data.Extraction("location", "US"),
            lx.data.Extraction("compensation", "$150k-$180k"),
            lx.data.Extraction("employment_type", "Full-time"),
            lx.data.Extraction("skill", "Python", attributes={"priority": "required"}),
            lx.data.Extraction("skill", "Airflow", attributes={"priority": "required"}),
            lx.data.Extraction("skill", "Kafka", attributes={"priority": "preferred"}),
        ],
    )
]


def extract_posting(text: str, callbacks: list | None = None) -> tuple[dict[str, Any], Any]:
    """Return (summary dict, raw AnnotatedDocument). The raw doc keeps char offsets
    so every fact can be traced back to the posting (see `save_highlight_html`)."""
    model = ClaudeLanguageModel(model_id=settings.model, callbacks=callbacks)
    doc = lx.extract(
        text_or_documents=text,
        prompt_description=PROMPT,
        examples=EXAMPLES,
        model=model,
        fence_output=True,
        use_schema_constraints=False,
    )
    return summarize(doc.extractions or []), doc


def summarize(extractions) -> dict[str, Any]:
    out: dict[str, Any] = {"required_skills": [], "preferred_skills": [], "facts": {}, "grounding": []}
    for e in extractions:
        span = e.char_interval
        out["grounding"].append({
            "class": e.extraction_class,
            "text": e.extraction_text,
            "start": getattr(span, "start_pos", None),
            "end": getattr(span, "end_pos", None),
        })
        if e.extraction_class == "skill":
            key = "preferred_skills" if (e.attributes or {}).get("priority") == "preferred" else "required_skills"
            if e.extraction_text not in out[key]:
                out[key].append(e.extraction_text)
        else:
            out["facts"].setdefault(e.extraction_class, e.extraction_text)
    return out


def save_highlight_html(doc, path: str) -> None:
    """LangExtract's interactive viewer: every extraction highlighted in the source."""
    html = lx.visualize(doc)
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text(html.data if hasattr(html, "data") else html)
