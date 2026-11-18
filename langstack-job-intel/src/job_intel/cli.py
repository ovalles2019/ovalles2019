"""Command line entry point.

    job-intel samples/posting_platform_engineer.txt samples/resume.example.md
    job-intel POSTING RESUME --auto-approve --guidance "emphasize Kubernetes"
    job-intel POSTING --extract-only --html out/highlights.html
"""

import argparse
import uuid
from pathlib import Path

from langgraph.types import Command

from . import observability
from .extraction import extract_posting, save_highlight_html
from .graph import build_graph


def main() -> None:
    p = argparse.ArgumentParser(prog="job-intel")
    p.add_argument("posting", type=Path)
    p.add_argument("resume", type=Path, nargs="?")
    p.add_argument("--extract-only", action="store_true", help="run LangExtract only")
    p.add_argument("--html", help="write LangExtract highlight viewer to this path")
    p.add_argument("--auto-approve", action="store_true", help="skip the interactive review")
    p.add_argument("--guidance", default="", help="reviewer guidance for tailoring")
    p.add_argument("--user", help="user id recorded on Langfuse traces")
    a = p.parse_args()
    posting = a.posting.read_text()

    if a.extract_only:
        summary, doc = extract_posting(posting, callbacks=observability.callbacks())
        print(summary)
        if a.html:
            save_highlight_html(doc, a.html)
            print(f"highlights -> {a.html}")
        observability.flush()
        return

    if not a.resume:
        p.error("resume is required unless --extract-only")

    graph = build_graph()
    config = observability.run_config(thread_id=str(uuid.uuid4()), user_id=a.user)
    result = graph.invoke({"posting": posting, "resume": a.resume.read_text()}, config)

    # LangGraph surfaces interrupt() payloads under "__interrupt__".
    while result.get("__interrupt__"):
        payload = result["__interrupt__"][0].value
        if a.auto_approve:
            decision = {"approved": True, "guidance": a.guidance}
        else:
            print(f"\nFit score {payload['score']}. Missing: {', '.join(payload['missing_skills']) or '-'}")
            approved = input(f"{payload['question']} [y/N] ").strip().lower() == "y"
            guidance = input("Guidance (optional): ").strip() if approved else ""
            decision = {"approved": approved, "guidance": guidance}
        result = graph.invoke(Command(resume=decision), config)

    print(result["report"])
    observability.flush()


if __name__ == "__main__":
    main()
