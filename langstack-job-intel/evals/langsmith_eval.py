"""LangSmith experiment: does extraction find the skills a human labeled?

    python evals/langsmith_eval.py              # creates the dataset on first run
    python evals/langsmith_eval.py --prefix effort-low

Each run is an experiment in LangSmith; compare runs side by side after changing
the prompt, few-shot examples, model or effort.
"""

import argparse
from pathlib import Path

from langsmith import Client

from job_intel.extraction import extract_posting

DATASET = "job-intel-extraction"
ROOT = Path(__file__).resolve().parents[1]

# Add more labeled postings here as you collect them.
EXAMPLES = [
    {
        "inputs": {"posting": (ROOT / "samples/posting_platform_engineer.txt").read_text()},
        "outputs": {
            "required_skills": ["Kubernetes", "Terraform", "Python", "GitHub Actions",
                                "Argo CD", "Prometheus", "Grafana"],
            "preferred_skills": ["EKS", "Go", "LangChain", "LangGraph", "pgvector"],
            "compensation": "$165,000 - $195,000",
        },
    },
]


def target(inputs: dict) -> dict:
    summary, _ = extract_posting(inputs["posting"])
    return summary


def _norm(xs):
    return {x.lower().strip() for x in xs}


def required_skill_recall(outputs: dict, reference_outputs: dict) -> dict:
    want = _norm(reference_outputs["required_skills"])
    found = _norm(outputs.get("required_skills", []) + outputs.get("preferred_skills", []))
    return {"key": "required_skill_recall", "score": len(want & found) / len(want)}


def skill_precision(outputs: dict, reference_outputs: dict) -> dict:
    found = _norm(outputs.get("required_skills", []) + outputs.get("preferred_skills", []))
    want = _norm(reference_outputs["required_skills"] + reference_outputs["preferred_skills"])
    return {"key": "skill_precision", "score": len(found & want) / len(found) if found else 0.0}


def compensation_exact(outputs: dict, reference_outputs: dict) -> bool:
    return outputs.get("facts", {}).get("compensation") == reference_outputs["compensation"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--prefix", default="extraction")
    args = ap.parse_args()
    client = Client()
    if not client.has_dataset(dataset_name=DATASET):
        client.create_dataset(DATASET, description="Labeled job postings for LangExtract")
        client.create_examples(dataset_name=DATASET, examples=EXAMPLES)
    client.evaluate(
        target,
        data=DATASET,
        evaluators=[required_skill_recall, skill_precision, compensation_exact],
        experiment_prefix=args.prefix,
    )


if __name__ == "__main__":
    main()
