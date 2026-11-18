"""Offline tests: the graph's routing and HITL behavior, with the LLM steps faked."""

import pytest
from langgraph.types import Command

from job_intel.extraction import summarize
from job_intel.graph import build_graph
from job_intel.schemas import FitAssessment, TailoredBullet, TailoredResume

FACTS = {"required_skills": ["Kubernetes", "Terraform"], "preferred_skills": ["Go"],
         "facts": {"seniority": "Senior"}, "grounding": []}


def fake_tailor(facts, fit, resume, guidance):
    return TailoredResume(
        bullets=[TailoredBullet(bullet=f"Ran EKS ({guidance})", targets_skill="Kubernetes",
                                source_line="Managed EKS clusters")],
        cover_note="Hello.")


def graph_with_score(score):
    fit = FitAssessment(score=score, matched_skills=["Kubernetes"], missing_skills=["Go"], rationale="r")
    return build_graph(extract_fn=lambda t: FACTS, assess_fn=lambda f, r: fit,
                       tailor_fn=fake_tailor, threshold=60)


CFG = {"configurable": {"thread_id": "t1"}}
INPUT = {"posting": "p", "resume": "r"}


def test_low_score_skips_review():
    out = graph_with_score(30).invoke(INPUT, CFG)
    assert "__interrupt__" not in out
    assert "tailoring skipped" in out["report"]


def test_high_score_pauses_then_tailors_on_approval():
    g = graph_with_score(85)
    out = g.invoke(INPUT, CFG)
    assert out["__interrupt__"][0].value["score"] == 85
    out = g.invoke(Command(resume={"approved": True, "guidance": "lead with IaC"}), CFG)
    assert "Ran EKS (lead with IaC)" in out["report"]


def test_high_score_declined():
    g = graph_with_score(85)
    g.invoke(INPUT, CFG)
    out = g.invoke(Command(resume={"approved": False}), CFG)
    assert "declined" in out["report"] and "tailored" not in out


@pytest.mark.parametrize("priority,key", [("required", "required_skills"), ("preferred", "preferred_skills")])
def test_summarize_buckets_skills(priority, key):
    import langextract as lx
    e = lx.data.Extraction("skill", "Python", attributes={"priority": priority})
    assert summarize([e])[key] == ["Python"]
