"""LangGraph layer: the stateful workflow with a human-in-the-loop checkpoint.

    extract -> assess_fit --(score < threshold)--> report
                          \\-(score >= threshold)-> human_review -> tailor -> report
                                                      (interrupt)

Node functions take their dependencies as arguments so tests can swap in fakes.
"""

from collections.abc import Callable
from typing import Any, TypedDict

from langgraph.checkpoint.memory import InMemorySaver
from langgraph.graph import END, START, StateGraph
from langgraph.types import interrupt

from . import chains, extraction
from .config import settings
from .schemas import FitAssessment, TailoredResume


class JobState(TypedDict, total=False):
    posting: str
    resume: str
    facts: dict[str, Any]
    fit: FitAssessment
    review: dict[str, Any]          # {"approved": bool, "guidance": str}
    tailored: TailoredResume
    report: str


def build_graph(
    extract_fn: Callable[[str], dict] | None = None,
    assess_fn: Callable[..., FitAssessment] | None = None,
    tailor_fn: Callable[..., TailoredResume] | None = None,
    threshold: int | None = None,
    checkpointer=None,
):
    extract_fn = extract_fn or (lambda text: extraction.extract_posting(text)[0])
    assess_fn = assess_fn or chains.assess_fit
    tailor_fn = tailor_fn or chains.tailor
    threshold = settings.fit_threshold if threshold is None else threshold

    def extract(state: JobState) -> JobState:
        return {"facts": extract_fn(state["posting"])}

    def assess_fit(state: JobState) -> JobState:
        return {"fit": assess_fn(state["facts"], state["resume"])}

    def route_after_fit(state: JobState) -> str:
        return "human_review" if state["fit"].score >= threshold else "report"

    def human_review(state: JobState) -> JobState:
        # Pauses the graph; state is checkpointed. Resume with
        # graph.invoke(Command(resume={"approved": True, "guidance": "..."}), config)
        decision = interrupt({
            "question": "Tailor the resume for this posting?",
            "score": state["fit"].score,
            "missing_skills": state["fit"].missing_skills,
        })
        return {"review": decision}

    def route_after_review(state: JobState) -> str:
        return "tailor" if state.get("review", {}).get("approved") else "report"

    def tailor(state: JobState) -> JobState:
        return {"tailored": tailor_fn(
            state["facts"], state["fit"], state["resume"], state["review"].get("guidance", ""))}

    def report(state: JobState) -> JobState:
        return {"report": render_report(state, threshold)}

    g = StateGraph(JobState)
    for name, fn in [("extract", extract), ("assess_fit", assess_fit),
                     ("human_review", human_review), ("tailor", tailor), ("report", report)]:
        g.add_node(name, fn)
    g.add_edge(START, "extract")
    g.add_edge("extract", "assess_fit")
    g.add_conditional_edges("assess_fit", route_after_fit, ["human_review", "report"])
    g.add_conditional_edges("human_review", route_after_review, ["tailor", "report"])
    g.add_edge("tailor", "report")
    g.add_edge("report", END)
    return g.compile(checkpointer=checkpointer or InMemorySaver())


def render_report(state: JobState, threshold: int) -> str:
    facts, fit = state["facts"], state["fit"]
    lines = ["# Job fit report", ""]
    for k, v in facts.get("facts", {}).items():
        lines.append(f"- **{k.replace('_', ' ').title()}:** {v}")
    lines += ["", f"## Fit score: {fit.score}/100 (threshold {threshold})", fit.rationale, "",
              f"**Matched:** {', '.join(fit.matched_skills) or '-'}",
              f"**Missing:** {', '.join(fit.missing_skills) or '-'}"]
    if fit.score < threshold:
        lines += ["", "_Below threshold: tailoring skipped._"]
    elif not state.get("review", {}).get("approved"):
        lines += ["", "_Reviewer declined tailoring._"]
    elif t := state.get("tailored"):
        lines += ["", "## Tailored bullets"]
        lines += [f"- {b.bullet}  \n  _targets: {b.targets_skill}; from: \"{b.source_line}\"_"
                  for b in t.bullets]
        lines += ["", "## Cover note", t.cover_note]
    return "\n".join(lines)
