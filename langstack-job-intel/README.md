# job-intel: one project, six Lang* tools

A job-posting intelligence agent. You give it a job posting and your resume. It:

1. **pulls out the hiring facts** from the posting (skills, pay, seniority, location), each tied to the exact text it came from,
2. **scores your fit** with Claude,
3. **stops and asks you** whether to tailor the resume, and
4. **rewrites your resume bullets** for that posting, citing the original line behind each one so nothing is made up.

Every run is traced. Extraction quality is measured with evals.

## Each tool has one job

| Layer | Tool | Its job in this project | Where |
|---|---|---|---|
| Extract | **LangExtract** | Turn free-text postings into structured facts with character offsets back to the source | `src/job_intel/extraction.py`, `claude_provider.py` |
| Build | **LangChain** | Prompt → Claude → typed Pydantic output (fit score, tailored bullets) | `src/job_intel/chains.py`, `llm.py` |
| Orchestrate | **LangGraph** | Stateful workflow: branching, checkpointing, human-in-the-loop `interrupt()` | `src/job_intel/graph.py` |
| Prototype | **Langflow** | Run the same graph as a drag-and-drop node to try prompts visually | `langflow/job_intel/` |
| Observe (dev) | **LangSmith** | Traces while you build, a labeled dataset, side-by-side experiments | `evals/langsmith_eval.py` |
| Observe (prod) | **Langfuse** | Traces per user and session, cost and latency dashboards, feedback scores | `src/job_intel/observability.py` |

```
posting ─▶ [extract: LangExtract] ─▶ [assess_fit: LangChain] ─┬─ score < 60 ─────────────────────▶ [report]
                                                               └─ score ≥ 60 ─▶ [human_review] ─▶ [tailor] ─▶ [report]
                                                                                 interrupt()
            └──────────────────────── LangGraph StateGraph + checkpointer ─────────────────────────┘
            every LLM call ─▶ LangSmith (auto)  +  Langfuse (callback handler)
```

### Design note: the original list has two overlaps

- **LangSmith and Langfuse do the same job.** Both trace LangChain and LangGraph runs, run evals, and manage prompts. A real team would pick one. This project splits them by stage so neither is redundant: LangSmith for building and evaluating, Langfuse for production monitoring. If you only want one, drop the other: the code runs with either, both, or neither configured.
- **Langflow and LangGraph are both orchestration layers.** Langflow is visual, LangGraph is code. Here, Langflow wraps the LangGraph graph instead of rebuilding it, so it is a prototyping front end and the workflow is defined once.
- **LangExtract isn't from the LangChain team.** It's Google's library and only supports Gemini, OpenAI and Ollama. `claude_provider.py` registers a provider that sends its calls through LangChain's `ChatAnthropic`, so extraction is traced like everything else.

## Quickstart

```bash
cd langstack-job-intel
python -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"
cp .env.example .env            # add ANTHROPIC_API_KEY (+ LangSmith / Langfuse keys if you want tracing)

pytest -q                       # offline tests, no API key needed

# 1. LangExtract only, with the interactive highlight viewer
job-intel samples/posting_platform_engineer.txt --extract-only --html out/highlights.html

# 2. Full graph, with the human review prompt
job-intel samples/posting_platform_engineer.txt samples/resume.example.md

# 3. Non-interactive, guidance passed to the tailoring step
job-intel samples/posting_platform_engineer.txt samples/resume.example.md \
  --auto-approve --guidance "lead with Kubernetes and IaC" --user oscar

# 4. LangSmith experiment on extraction quality
python evals/langsmith_eval.py --prefix baseline

# 5. Langflow
pip install langflow
LANGFLOW_COMPONENTS_PATH=$(pwd)/langflow langflow run   # look for "Job Intel" in the sidebar
```

## Models

All calls use `claude-opus-5-5` through `langchain-anthropic`, with effort set per step:

| Step | Effort | Why |
|---|---|---|
| Extraction | `low` | Matching against few-shot examples, high volume |
| Fit scoring | `medium` (configurable) | Judgment, short output |
| Tailoring | `high` | Writing that must stay faithful to the resume |

Structured outputs use `with_structured_output(..., method="json_schema")`. The default `function_calling` method forces a tool call, which Opus 5.5 rejects.

## Ideas for what to add next

- **Batch mode:** run a folder of postings through the graph and rank them by fit score.
- **Langfuse feedback:** record a 👍/👎 on each tailored resume as a Langfuse score, and use the low-rated ones as new LangSmith eval examples.
- **Retrieval:** store past postings in pgvector and add a "similar roles" node.
- **Persistence:** swap `InMemorySaver` for `langgraph-checkpoint-postgres` so a paused review survives a restart.
