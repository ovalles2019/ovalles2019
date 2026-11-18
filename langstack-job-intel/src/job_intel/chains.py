"""LangChain layer: prompt | model | structured-output chains."""

import json

from langchain_core.prompts import ChatPromptTemplate

from .llm import get_llm
from .schemas import FitAssessment, TailoredResume

# method="json_schema" uses Claude's native structured outputs. The default
# ("function_calling") forces a tool call, which Opus 5.5 rejects.

FIT_PROMPT = ChatPromptTemplate.from_messages([
    ("system",
     "You are a technical recruiter. Score how well the resume fits the posting. "
     "Count a skill as matched only if the resume shows concrete evidence of it. "
     "Weight required skills about three times as much as preferred ones."),
    ("human", "POSTING FACTS (extracted):\n{facts}\n\nRESUME:\n{resume}"),
])

TAILOR_PROMPT = ChatPromptTemplate.from_messages([
    ("system",
     "Rewrite resume bullets to target this posting. Never invent experience: "
     "every bullet must come from a real resume line, quoted in source_line. "
     "Reviewer guidance, if any, overrides your defaults."),
    ("human",
     "POSTING FACTS:\n{facts}\n\nFIT ASSESSMENT:\n{fit}\n\nRESUME:\n{resume}\n\n"
     "REVIEWER GUIDANCE:\n{guidance}"),
])


def fit_chain():
    return FIT_PROMPT | get_llm().with_structured_output(FitAssessment, method="json_schema")


def tailor_chain():
    return TAILOR_PROMPT | get_llm("high").with_structured_output(TailoredResume, method="json_schema")


def assess_fit(facts: dict, resume: str, config=None) -> FitAssessment:
    return fit_chain().invoke({"facts": json.dumps(facts, indent=2), "resume": resume}, config=config)


def tailor(facts: dict, fit: FitAssessment, resume: str, guidance: str, config=None) -> TailoredResume:
    return tailor_chain().invoke(
        {"facts": json.dumps(facts, indent=2), "fit": fit.model_dump_json(indent=2),
         "resume": resume, "guidance": guidance or "(none)"},
        config=config,
    )
