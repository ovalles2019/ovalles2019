# Hour zero: the shared kit

Build this before Day 1. Budget 45 minutes. It is deliberately small — four modules,
maybe 250 lines total. Ten of the fifteen days import it, and it's the reason
one-project-per-day is feasible at all: without it you rebuild provider plumbing and
scoring every morning and lose 40 minutes a day to it.

Resist making it good. It is a lab instrument, not a library. No packaging, no tests,
no abstractions you don't need on Day 1.

---

## `kit/providers.py`

The single most reused thing you will write. **Every cost, latency, and token number in
the next fifteen days comes out of here**, so get the `Response` shape right and never
touch it again.

```python
@dataclass
class Response:
    text: str
    model: str
    in_tokens: int
    out_tokens: int
    latency_ms: float
    cost_usd: float
    raw: dict          # keep it — Day 5 tracing and Day 12 confidence want the details
```

```python
def complete(prompt, *, model="gpt-4o-mini", system=None, temperature=0.0,
             json_schema=None) -> Response
```

Requirements:

- Dispatch to OpenAI / Anthropic / Ollama off a `model` string prefix.
- **Compute `cost_usd` from a price table you look up today.** Hardcode it in a dict at
  the top of the file. Day 7 and Day 9 are both worthless if this number is fabricated.
- Retry on 429 and 5xx with exponential backoff. You'll hit rate limits on Days 2, 7 and 8.
- `json_schema` → use the provider's structured-output mode. Days 3, 5, 6, 11, 12 and 15
  all want typed output; `instructor` on top of this is fine too.
- An `acomplete()` async variant. Days 2, 6, 7 and 8 all batch, and serial batching will
  eat your build hour.

## `kit/judge.py`

LLM-as-judge, used on nine of the fifteen days.

```python
@dataclass
class Score:
    value: int          # 1-5
    reasoning: str
    judge_model: str

def judge(output: str, criteria: str, *, reference: str | None = None,
          rubric: str | None = None) -> Score
```

Two things that matter more than they look:

- **Always capture `reasoning`.** On Day 5 and Day 13 the reasoning *is* the deliverable,
  not the score.
- **Pin the judge model and record it in the `Score`.** If you silently change judges
  mid-plan, every cross-day comparison you make becomes meaningless — and noticing that
  trap is itself a good interview answer.

Add `judge_pairwise(a, b, criteria) -> Literal["a", "b", "tie"]` while you're here.
Day 14's base-vs-tuned comparison wants it, and pairwise preference is more reliable than
two independent absolute scores.

## `kit/trace.py`

A decorator that records what a function did. Days 5, 9 and 15.

```python
@span("extract_entities")
def extract(doc: Document) -> Entities: ...
```

Captures per call: step name, serialized input, serialized output, the prompt sent, the
raw response, token counts, latency, exception if any, and a parent trace id so calls
nest. Write completed traces to `traces/<trace_id>.json` and index
`(trace_id, timestamp, status)` in SQLite.

Use a `contextvars.ContextVar` for the current trace id so nested spans link up without
you threading an argument through every function. That's about ten lines and it's what
makes Day 5 pleasant instead of miserable.

## `kit/store.py`

SQLite. No ORM, no migrations, no Alembic.

```sql
CREATE TABLE runs   (id, day, started_at, config_json, notes);
CREATE TABLE cases  (id, day, input_json, expected_json, tags, source, created_at);
CREATE TABLE results(run_id, case_id, output_json, scores_json,
                     cost_usd, latency_ms, passed);
```

That's enough for every day. `cases.source` matters more than it looks — on Day 2 you
start appending machine-generated cases next to your hand-written ones, and you need to
be able to tell them apart forever after.

Give it one convenience function:

```python
def diff_runs(run_a, run_b) -> {"regressions": [...], "improvements": [...], "delta": ...}
```

Day 1 needs it, Day 2 reuses it, Day 3 compares variants with it, and Day 14 compares
base against tuned with it. Writing it once on Day 1 saves you three rewrites.

---

## Sanity check before Day 1

Ten minutes, and it will save you an hour later:

```python
r = complete("Say OK", model="gpt-4o-mini")
assert r.cost_usd > 0 and r.latency_ms > 0 and r.in_tokens > 0
```

If `cost_usd` is zero or wrong, stop and fix it now. Half the numbers in this plan are
downstream of that field.
