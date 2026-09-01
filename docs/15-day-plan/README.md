# 15 Days, 15 Core Mechanisms

A daily build plan derived from *15 AI Engineering Projects That Actually Land Jobs*
(BASWE / AiEngineerAccelerator), adapted for one project per day.

---

## Read this first: the honest problem with "one project a day"

The source guide scopes every project at **12–14 days of focused work (2–3 hrs/day)** —
roughly 30 hours each. Fifteen of those is 450 hours. You have about 40.

The guide also says, on page one:

> "You don't need to build all fifteen. Pick the two or three that best align with the
> roles you're targeting and build those exceptionally well. Two deep, polished projects
> beat five shallow ones every time."

So a literal reading of "complete a project every day" produces fifteen shallow demos —
exactly what the guide warns against, and exactly what your portfolio does *not* need
(you already have seven real repos).

But your actual stated goal is not "ship fifteen portfolio pieces." It is:

> "I really need to sharpen my skills and be able to explain and learn more."

That is a **learning** goal, and for a learning goal, breadth-first is the right call.
So this plan does something different.

### What you build instead

Every day you build the **one core mechanism** that makes that project non-obvious —
the load-bearing 20% that you cannot understand by reading about it. You skip every
wrapper the guide adds for portfolio polish: no Streamlit UI, no Docker Compose, no
Grafana, no FastAPI, no README-as-case-study. Those are packaging. They teach you
almost nothing you don't already know from your CI/CD and cloud work.

Then you spend the last 30 minutes writing down what you learned, in your own words,
and saying it out loud. That is the part that makes you able to *explain* it, which is
what you actually asked for.

**Day 16 is the payoff.** After fifteen days you will have touched every dimension of
AI engineering and you will know, from experience rather than guessing, which two you
want to own. Then you run the guide's *full* 12–14 day plan on those two. That is how
this plan and the guide's advice stop contradicting each other.

| | |
|---|---|
| **Days 1–15** | 15 vertical slices. Breadth. Learning. ~2.5 hrs/day. |
| **Day 16** | Pick 2. |
| **Days 17–45** | The guide's real plan, on those 2. Depth. Portfolio. |

---

## The daily rhythm

Roughly 2.5 hours. The block structure matters more than the exact minutes.

**1. Frame it — 15 min.** Read that project's section in the guide. Before writing any
code, answer in one sentence: *what does this system do that the naive version doesn't?*
Write it at the top of the day's `EXPLAIN.md`. If you can't answer it, you don't yet
understand what you're building — re-read.

**2. Build the slice — 90 min.** Only the core mechanism listed for that day. Nothing
else. Hard stop.

**3. Prove it — 20 min.** Produce **one number**. Not a feeling, a number: a pass rate,
a p-value, a cost delta, a catch rate. Every day below names the number to produce.
Deliberately break something and watch the mechanism catch it — a regression detector
that has never caught a regression has not been tested.

**4. Teach it back — 30 min.** Fill in `EXPLAIN.md` (template in this folder) and then
**record yourself explaining it out loud in 90 seconds, without notes.** Voice memo is
fine; nobody will hear it. This is non-negotiable — writing something down and being
able to say it are different skills, and interviews test the second one.

Then commit and push. One commit per day, message = the number you produced.

### The scope rules

Read these on any day you feel yourself sliding.

- **Definition of done:** it runs end-to-end from one command, it produced one number,
  `EXPLAIN.md` is filled in, it's pushed. That's it.
- **Hard stop at 3 hours.** If it isn't working, commit what runs and write the
  teach-back on what you learned *including why it didn't finish*. Then move on.
  A broken Day 7 that you understand beats a perfect Day 7 that eats Day 8.
- **Never build a UI.** Print to stdout. Write a JSON file. If you need to look at
  something, `pandas.DataFrame(...).to_markdown()`.
- **Never containerize.** You already know Docker. It teaches you nothing here.
- **Small models for bulk, strong model for judging.** `gpt-4o-mini` / `claude-haiku`
  for the thing under test; a strong model only where you're using LLM-as-judge.
- **Hand-write the small datasets.** When a day says "write 25 test cases by hand,"
  write them by hand. Generating your ground truth with an LLM means you're measuring
  the LLM against itself. This is the single most common mistake in AI eval work and
  the guide calls it out explicitly on Day 1.

---

## Hour zero: the shared kit

Before Day 1, spend 45 minutes building `kit/` — a tiny shared library that ten of the
fifteen days import. This is what makes one-project-per-day feasible at all: without it
you'd rebuild provider plumbing every morning.

Full spec in [`kit-spec.md`](./kit-spec.md). It is four small modules:

- `kit/providers.py` — one `complete()` call across OpenAI/Anthropic/Ollama that always
  returns `Response(text, in_tokens, out_tokens, latency_ms, cost_usd, model)`.
  **Every cost and latency number in the next 15 days comes from here.**
- `kit/judge.py` — `judge(output, criteria, rubric) -> Score(1-5, reasoning)`.
  Used on days 1, 2, 3, 5, 6, 7, 10, 12, 14.
- `kit/trace.py` — a `@span` decorator that records input, output, prompt, tokens,
  latency, and error for any function. Days 5, 9, 15.
- `kit/store.py` — SQLite. `runs`, `cases`, `results`. No ORM, no migrations.

### Repo layout

One monorepo while you're learning, so the kit is shared:

```
ai-eng-15/
  kit/           # shared, built at hour zero
  day01_regression/
    run.py
    EXPLAIN.md
    result.json
  day02_evalgen/
  ...
  day15_agents/
  FLASHCARDS.md  # grows daily, one card per day
```

On Day 16, extract the two you're deepening into their own standalone repos. Recruiters
see two clean, deep repos; you keep the monorepo as your lab notebook.

---

## The 15 days

Ordered so each day reuses the one before it. This is not the guide's numbering — it's
resequenced so the dependencies run forward, and so the days that overlap your existing
repos (RAG, agents, MCP) land later, when you can move through them faster.

| Day | Guide | Build | The number |
|:---:|:------|:------|:-----------|
| 1 | P1 | Golden set + eval runner + regression diff | Regressions caught between 2 prompt versions |
| 2 | P13 | Cluster logs → auto-label → grow Day 1's golden set | Auto-label agreement on a 20-case spot check |
| 3 | P9 | Prompt registry + traffic splitter + t-test | p-value, and whether you'd ship |
| 4 | P12 | Quality-gated rollout + auto-rollback | Blast radius before rollback fired |
| 5 | P3 | Span tracing + backward root-cause analyzer | Root-cause hit rate on 20 seeded failures |
| 6 | P5 | 3 critics on 3 models + adjudicator ⟨+ review⟩ | Issues caught by 3 models vs 1 model 3× |
| 7 | P2 | Complexity classifier + cost routing + async verifier | % cost saved at what quality parity |
| 8 | P7 | Semantic cache + similarity threshold sweep | The hit-rate / wrong-answer crossover point |
| 9 | P11 | Token bucket + circuit breaker + fallback chain | Requests survived during simulated outage |
| 10 | P6 | BM25 + RRF + reranker + citation verification | Faithfulness: hybrid vs dense-only |
| 11 | P8 | Text-to-SQL guardrails + back-translation ⟨+ review⟩ | Unsafe queries executed (target: 0) |
| 12 | P14 | Dual-engine OCR + typed extraction + confidence routing | Auto-approval rate at 100% precision |
| 13 | P4 | Code↔doc link graph + staleness verification | False-positive rate on 10 commits |
| 14 | P10 | LoRA fine-tune + base-vs-tuned head-to-head | Task lift, and general-capability loss |
| 15 | P15 | Supervisor + specialists + memory + escalation ⟨+ review⟩ | Cost of run 1 vs run 2 (memory payoff) |

Days 6, 11 and 15 open with a 20-minute closed-book review (see below). Day 14 is the
one day that will exceed 3 hours of wall-clock time — put it on a weekend.

---

### Day 1 — Model Regression Detection (Guide P1)

**Build.** The customer-support email classifier from the guide, with the prompt in a
versioned YAML file. **40** hand-written test cases (the guide says 50–100; 40 is enough
to see signal and fits the day) with deliberate edge cases: ambiguous between two
categories, three words long, typo-ridden, sarcastic. An eval runner that scores exact
category match *and* summary quality via `kit.judge`. Then the part that matters: diff
this run against the previous run and list the cases that flipped pass→fail.

**Skip.** Slack, the HTML report, drift detection, Docker, Streamlit. Do wire up the
GitHub Action if you have 15 minutes left — that part is genuinely 15 minutes for you.

**Prove.** Change one line of the prompt to make it worse. Re-run. The diff must name
the specific cases that broke. Record: pass rate before, after, and the regression list.

**Explain.** Why must the golden set be hand-labeled? What's the difference between a
per-run regression and slow drift, and why does catching the second one need different
machinery? Why is 2-of-40 flipping possibly noise?

---

### Day 2 — Eval Dataset Generator from Logs (Guide P13)

**Build.** Reuses Day 1 directly. Generate ~300 varied support emails as synthetic
"production traffic," run them through Day 1's classifier, log every interaction.
Then: embed the prompts, cluster with HDBSCAN, flag the outliers (things that don't
cluster = novel requests) and the low-confidence responses. Auto-label the interesting
ones with a strong model. **Dedupe against Day 1's golden set at cosine > 0.92.** Append
the survivors.

**Skip.** The curation dashboard, cron scheduling, PII redaction, ClickHouse, the
difficulty estimator.

**Prove.** How many new cases survived dedupe? Then hand-label 20 of them yourself and
measure agreement with the auto-labels. That disagreement rate is the whole story.

**Explain.** Why is the dataset the bottleneck in eval, not the harness? What does
confidence-based routing to human review buy you? Why does deduplication matter more
than it sounds?

---

### Day 3 — Prompt Versioning & A/B Testing (Guide P9)

**Build.** A prompt registry: id, version, parent version, text, params, commit message.
A traffic splitter using **consistent hashing on user id** so the same user always sees
the same variant. Run 500 requests across two genuinely different variants (zero-shot vs
few-shot). Then `scipy.stats`: two-sample t-test, p-value, confidence interval, and the
minimum detectable effect at your current n.

**Skip.** Postgres (SQLite is fine), the dashboard, auto-promotion, template variables,
the audit log.

**Prove.** A p-value and a decision. Say out loud whether you'd ship the variant, and
why — including the case where the variant looks better but isn't significant.

**Explain.** Why consistent hashing rather than `random()`? What does a p-value of 0.03
*not* mean? What's the minimum detectable effect and why does it stop you from running
underpowered experiments forever?

---

### Day 4 — AI Feature Flag with Auto-Rollback (Guide P12)

**Build.** Reuses Day 3's splitter. A flag config carrying a rollout percentage, a
quality threshold, and a rollback trigger. `evaluate(flag, user_ctx)` returns a variant.
An async scorer that rates every gated output *after* the response is returned. A rolling
window over the last 100 evaluations tracking mean, P10, and trend. Then the trigger:
when P10 drops below threshold for N consecutive evaluations, force rollout to 0% and log
it. **Plant a deliberately bad variant and watch it get rolled back.**

**Skip.** Targeting rules, shadow mode, the dashboard, Redis, the SDK packaging.

**Prove.** How many requests were served the bad output before rollback fired? That's
your blast radius. Tune the window and threshold and watch it change.

**Explain.** Why do AI features fail on a gradient rather than a binary, and why does
that break traditional feature flags? Why monitor P10 instead of the mean? What is
flapping and why does the cooldown exist?

---

### Day 5 — Failure Forensics (Guide P3)

**Build.** A 4-step pipeline: intake → extract entities → classify doc type →
summarize. Each step typed with Pydantic. Wrap each in `kit.trace`'s `@span` so you
capture input, output, the exact prompt, tokens, latency, and a self-reported confidence
score. Then the analyzer: walk the spans **backward**, LLM-judge whether each step's
output is a reasonable transformation of its input, and name the first big quality drop
as the root cause. Classify it into the guide's taxonomy (extraction hallucination,
misclassification, propagation error, prompt failure, context loss).

**Skip.** The React trace explorer, OpenTelemetry export, the flagging UI, the
feedback-to-eval loop.

**Prove.** Seed 20 documents where you *know* which step will break. How often does the
analyzer finger the right step?

**Explain.** What's the difference between an origin error and a propagation error, and
why does that distinction change where you invest engineering effort? Why is a model's
self-reported confidence weak evidence — and why is it still worth capturing?

---

### Day 6 — Output Arbitration ⟨+ Review⟩ (Guide P5)

**First 20 minutes: closed-book review.** Pick three of Days 1–5 at random. Re-record
the 90-second explanation for each without looking at your notes. Then read what you
wrote and mark what you got wrong. That gap is your real knowledge.

**Build.** You already know LangGraph, so go straight at the interesting part. Three
critics — factual accuracy, logical consistency, completeness — each on a **different
model**, each returning a Pydantic critique via `instructor`. Dispatch them in parallel.
Then a disagreement detector (severity differs by >2, or one critic found something the
others entirely missed) and an adjudicator that resolves conflicts with reasoning.

**Skip.** The verdict explorer UI, batch mode, FastAPI, the critic analytics.

**Prove.** Run the panel on a text with planted errors. Then run *one* model three times
in the three critic roles. How many issues does the multi-model panel catch that the
single-model panel misses? That delta is the entire argument for the architecture.

**Explain.** Why does same-model self-critique systematically fail? Why is disagreement
between critics the valuable signal rather than the noise?

---

### Day 7 — LLM Cost Autopilot (Guide P2)

**Build.** A model registry with **real current prices** — go look them up, don't guess.
Hand-label 150 prompts into the guide's three complexity tiers. Extract cheap features
(token count, imperative verbs like "analyze"/"compare", constraint count, whether
context is supplied, output format complexity) and train a logistic regression. 80%
held-out accuracy is fine. A tier→model map in YAML. Then the async verifier: after
returning the cheap model's answer, re-run on the top model and score agreement.

**Skip.** FastAPI, the dashboard, the weekly retraining loop, Ollama.

**Prove.** Run 200 mixed prompts. Report: dollars spent routed, dollars if everything
went to the top model, and the quality-agreement rate. "Saved 62% at 94% agreement" is
the shape of the answer.

**Explain.** Where is the cost/quality frontier and how do you choose a point on it?
Why must the verifier run async? What triggers escalation, and what does escalation cost
you in latency?

---

### Day 8 — Semantic Caching (Guide P7)

**Build.** Reuses Day 7's provider layer. Embed each incoming prompt, look up the nearest
neighbour, hit if cosine > threshold. Store the response with metadata. The subtle part
the guide flags: **the cache key must include the system-prompt hash, temperature, and
model** — otherwise two different features silently poison each other's cache. Replay 500
queries with realistic repetition and paraphrase.

**Skip.** Redis (in-memory or `sqlite-vec` is fine), streaming, Prometheus/Grafana, TTL
tiers, the near-miss analyzer.

**Prove.** Sweep the threshold from 0.85 to 0.99. For each: hit rate, and the rate at
which a "hit" returned an answer that was actually wrong for the new question. Plot the
two curves and find the crossover. **That curve is the deliverable.**

**Explain.** Why can't you exact-match cache LLM calls? Why is the similarity threshold a
product decision rather than a technical one — and who should own it? How does cache
contamination happen across features?

---

### Day 9 — LLM Gateway (Guide P11)

**Build.** This is your home turf — lean into it and go deep on the resilience patterns
rather than the plumbing. A token-bucket rate limiter per team. A budget cap that blocks
at 100% and warns at 80%. A health checker that maintains healthy/degraded/down per
provider. A fallback chain defined **per tier, not per model**. And a circuit breaker
with a proper half-open state.

**Skip.** OpenTelemetry, Grafana, streaming passthrough, the admin API, request
enrichment, rewriting it in Go.

**Prove.** Simulate a provider outage and a 429 storm. Measure: gateway overhead in ms,
and how many requests completed successfully with fallback enabled vs disabled.

**Explain.** Token bucket vs leaky bucket vs fixed window — when does each fail? Walk
through the three circuit-breaker states and what closes the circuit again. Which errors
are retryable and which must never be retried?

---

### Day 10 — RAG with Hybrid Search (Guide P6)

**Build.** You have `pgvector-rag-demo` — do not rebuild ingestion, chunking, or dense
retrieval. Start from a working dense retriever and add only the delta: a BM25 index over
the same chunks, **Reciprocal Rank Fusion** to merge the two ranked lists, a cross-encoder
reranker over the top 20 keeping the top 5, grounded generation with `[n]` citations, and
a verification pass that checks each citation actually supports the claim attached to it.

**Skip.** Multi-format loaders, the chunking-strategy bake-off, dedup, the API, the
dashboard, the "I don't know" handler (unless it's quick).

**Prove.** 25 hand-written Q&A pairs including two multi-hop and two unanswerable.
Measure faithfulness and citation accuracy for hybrid vs dense-only. Report both.

**Explain.** Why does BM25 still beat embeddings for error codes, config keys, and
function names? Explain RRF in one sentence. What does a cross-encoder do that a
bi-encoder structurally cannot?

---

### Day 11 — Text-to-SQL with Guardrails ⟨+ Review⟩ (Guide P8)

**First 20 minutes: closed-book review** of three days from 1–10.

**Build.** SQLAlchemy introspection → a schema representation with FK relationships and
sample values for categorical columns. A prompt builder that includes only relevant
tables. Then the two layers that matter: **guardrail middleware** (block all DDL and DML,
force a `LIMIT`, cap subquery depth, reject queries whose `EXPLAIN` estimates a huge
scan) and **a read-only DB role plus an auto-rollback transaction**. Then back-translation:
ask the model what question the generated SQL answers, and compare it to the original.

**Skip.** The frontend, multi-query validation, the feedback loop, schema embedding
filters, a 50-case eval (do 15).

**Prove.** Write 15 adversarial prompts explicitly trying to get a `DROP` or `UPDATE`
through. Target: zero unsafe queries executed. Then measure how many bad translations the
back-translation check catches.

**Explain.** Why keep the read-only DB role even when your middleware is correct — what
is defense in depth actually buying? Why is back-translation a cheap hallucination check,
and where does it fail?

---

### Day 12 — Document Processor (Guide P14)

**Build.** Check text density per PDF page to auto-choose native extraction vs OCR. Run
**both Tesseract and EasyOCR** and use their agreement as a confidence signal — that
ensemble trick is the interesting idea here. Then `instructor` + a Pydantic `Invoice`
model for structured extraction, with **per-field confidence** (did the value appear
verbatim in the OCR text? did chunks agree? does it parse?). Route on confidence:
auto-approve, fast review, or detailed review.

**Skip.** The preprocessing pipeline, Celery, the review UI, the anomaly detector, the
correction feedback loop. Try the vision-model fallback on three hard pages only.

**Prove.** On 20 documents, find the confidence threshold at which auto-approval is 100%
precise, and report what percentage of documents clear it. **That percentage is the
business case.**

**Explain.** Why is per-field confidence more useful than one document-level score? How
would you choose the auto-approve threshold from the business cost of a wrong extraction
rather than from the model's numbers?

---

### Day 13 — Self-Healing Documentation (Guide P4)

**Build.** Point it at your own `cicd-pipeline-demo`. Parse functions, classes, and CLI
commands into chunks with stable ids. Parse the README into sections by heading. Build the
link graph — name matching first, then embeddings for the rest. On a diff, map changed
lines to code chunks, filter out comment/whitespace/test-only changes, find linked doc
sections, and **LLM-verify** whether each is actually now stale (this filter is what stops
the tool from being unusable). Generate a targeted patch that rewrites only the stale
parts.

**Skip.** The marketplace publish, the Docker action, auto-merge, the TypeScript version,
forking FastAPI to test on.

**Prove.** Make 10 commits, 5 that genuinely invalidate docs and 5 that don't. Measure
true positives, false positives, false negatives. **The false-positive rate is the number
that matters.**

**Explain.** Why do false positives kill a developer tool faster than false negatives?
Why is "rewrite only the stale parts, preserve everything else" a much harder prompt than
"rewrite this doc"?

---

### Day 14 — LoRA Fine-Tuning (Guide P10) — *put this on a weekend*

The one day with real setup cost and unattended wall-clock time. Training runs while you
do something else — **write the teach-back while the GPU works.**

**Build.** Pick one narrow, measurable task (the guide's suggestions: code-review comment
generation, support tone matching, clause classification). Build 400–600 instruction-format
examples, split 80/10/10 with no leakage. QLoRA via Unsloth on a **small** base model —
Llama-3.2-1B/3B or Qwen2.5-1.5B — on a free Colab T4. Rank 16, alpha 32, dropout 0.05.
Then the part that actually matters: run the **base** model on your benchmark first to
establish the denominator, then the tuned model on the identical benchmark with identical
scoring.

**Skip.** A full hyperparameter sweep (run 2 configs, not 9), W&B if it slows you down,
vLLM, lm-eval-harness, deployment, the A/B endpoint.

**Prove.** Base % vs tuned % on 30 hand-made eval cases. Then a small
catastrophic-forgetting probe: 10 general questions, both models, did the tuned one get
worse?

**Explain.** When does fine-tuning beat RAG or few-shot — and when does it lose? (Hint:
it's about form, style, and format, not facts.) What do rank and alpha actually control?
Why is the test set sacred?

---

### Day 15 — Agent Orchestration ⟨+ Final Review⟩ (Guide P15)

**Build.** You have `agentic-ai-workflows` and `aws-cloudops-mcp`, so skip the framework
tour. A supervisor that decomposes a request into a **typed, validated plan** with
dependencies. Two specialists with three real tools between them (reuse your MCP work).
A reviewer node that can send work back with feedback. An escalation trigger on low plan
confidence or a sensitive action. Working memory for the run, and **long-term memory
written at the end and retrieved into the next run's planning prompt.**

**Skip.** The replay system, the trace explorer UI, Celery, memory consolidation and
decay, four approval levels (build two).

**Prove.** Run a task. Then run a *similar* task and show that retrieved memory changed
the plan. Report token cost and step count for run 1 vs run 2.

**Explain.** Why does a supervisor + specialists beat one large agent with all the tools?
Where exactly should human-in-the-loop sit, and what does it cost? What does memory
retrieval concretely change about the plan?

**Then: the final review, and the Day 16 decision** (below).

---

## The review checkpoints

Fifteen days of building without review means you'll have forgotten Day 2 by Day 12. The
20-minute blocks on Days 6, 11 and 15 are what turn this from a build streak into
learning.

Each checkpoint: pick three earlier days at random. **Closed book**, re-record the
90-second explanation for each. Then compare against what you wrote. Whatever you
couldn't reconstruct is what you don't actually know — flag it in `FLASHCARDS.md` and
re-read that day's `EXPLAIN.md`.

The Day 15 review covers all fifteen. Budget an hour for it, not twenty minutes.

---

## Day 16: choose your two

Pick using these signals, in order:

1. **Which were hardest to explain?** Difficulty explaining something means there's real
   depth there you haven't reached — and that's where deep work pays off most.
2. **Which match the roles you're targeting?** Eval/observability roles → Days 1, 2, 5.
   Platform/infra roles → Days 7, 8, 9. Applied AI product roles → Days 10, 11, 12.
   ML-leaning roles → Day 14.
3. **Which don't duplicate your existing repos?** You already demonstrate RAG, agentic
   workflows, MCP, and CI/CD. Days 1, 7, 8, 9, 13 and 14 cover ground your portfolio
   currently doesn't.

Then run the guide's actual 12–14 day plan on those two — all six phases, including the
polish phase you skipped every day: the Docker Compose, the dashboard, the Loom
walkthrough, the README written as onboarding docs, the case study that leads with a
number.

Those two become portfolio pieces. The other thirteen stay in the monorepo as your lab
notebook — and, more importantly, as thirteen things you can now talk about fluently when
an interviewer wanders off-script.

---

## Logistics

**API cost.** Roughly $2–4 on a typical day, more on Days 2, 7 and 8 where you're pushing
hundreds of requests through. Budget **$40–50 for the fifteen days.** Keep it down by
using small models for the system under test and reserving a strong model for judging.
Set a hard spend limit in your provider dashboard on day one.

**GPU (Day 14 only).** Free Colab T4 is sufficient for a 1B–3B model with QLoRA. If
Colab is being stingy, RunPod or Modal will cost about $1–2 for the run. Do not attempt
Llama 3 8B on a free tier — the guide suggests it, but a smaller model demonstrates the
identical concepts and finishes inside your day.

**Local fallback.** Install Ollama at hour zero with one small model pulled. On days when
you're iterating on plumbing rather than quality, point `kit/providers.py` at it and your
API spend goes to zero.

**Falling behind.** If you miss a day, skip it rather than doubling up — doubling up is
how the streak dies. The sequence tolerates gaps everywhere except Day 2 (needs Day 1)
and Day 8 (wants Day 7). If you miss several, the ordering is roughly priority-ordered
for your specific gaps already.

---

## Files in this folder

- [`EXPLAIN-template.md`](./EXPLAIN-template.md) — copy into each day's folder. This is
  the mechanism that turns building into explaining.
- [`kit-spec.md`](./kit-spec.md) — the shared library to build at hour zero.
