"""Typed contracts passed between graph nodes."""

from pydantic import BaseModel, Field


class FitAssessment(BaseModel):
    score: int = Field(ge=0, le=100, description="Overall fit, 0-100")
    matched_skills: list[str] = Field(description="Required skills the resume demonstrates")
    missing_skills: list[str] = Field(description="Required skills with no resume evidence")
    rationale: str = Field(description="Two or three sentences explaining the score")


class TailoredBullet(BaseModel):
    bullet: str = Field(description="Resume bullet rewritten for this posting")
    targets_skill: str = Field(description="The posting requirement this bullet addresses")
    source_line: str = Field(description="The original resume line it was derived from")


class TailoredResume(BaseModel):
    bullets: list[TailoredBullet]
    cover_note: str = Field(description="A three-sentence note to the hiring manager")
