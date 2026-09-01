# Day NN — <project name>

> Copy this file into `dayNN_<slug>/EXPLAIN.md` and fill it in during the last 30 minutes.
> Write it for a version of you six months from now who has forgotten everything.
> Prose, not bullets — bullets let you skip the connective reasoning, and the connective
> reasoning is the part interviews test.

---

## 0. The framing sentence

*Written BEFORE you started building, in the first 15 minutes.*

What does this system do that the naive version doesn't?

<!-- One sentence. If you can't write it, you haven't understood the project yet. -->

---

## 1. The problem underneath it

What actually goes wrong in a real team without this? Be concrete — a specific bad
Tuesday, not "quality issues." Who notices first, and how long does it take them?

---

## 2. The data flow, in five boxes

Draw it. ASCII is fine. Every arrow gets a label saying what data crosses it.

```
[ ] --> [ ] --> [ ] --> [ ] --> [ ]
```

Now, in a sentence each: what does every box do, and what would break downstream if it
were removed?

---

## 3. The decision I made and the one I rejected

Every day has at least one fork where you picked something. Name it, name the alternative,
and say what you traded away. "I didn't think about it" is a valid answer *today* — but
then go back and think about it, because this is the question that separates a candidate
who built something from one who followed a tutorial.

**I chose:**

**Over:**

**The tradeoff:**

---

## 4. How it fails

What breaks first at 100× the volume? What's the failure mode nobody would notice for a
week? Where does this system give a confidently wrong answer rather than an error?

---

## 5. The number

What did you measure, what came out, and — importantly — **what would have made you
decide differently?**

| | |
|---|---|
| Metric | |
| Result | |
| Threshold where my conclusion flips | |

---

## 6. The 90-second version

Write out what you're going to say, then **record yourself saying it without reading**.

Structure that works: *the problem in one sentence → what you built in two → the one
design decision you're proud of → the number.* Lead with the problem, never with the
tech stack.

<!-- Then: voice memo, 90 seconds, no notes. Listen back once. If you said "um" a lot or
     lost the thread halfway, that's the signal — record it again. -->

- [ ] Recorded
- [ ] Listened back

---

## 7. Flashcards

Two or three questions you'd want to be asked about this, with answers. Append these to
the root `FLASHCARDS.md` — they're what you re-test yourself on at the Day 6, 11 and 15
checkpoints.

**Q:**
**A:**

**Q:**
**A:**

---

## 8. What I got wrong

What did you believe at 9am that turned out to be false by noon? This section is the most
valuable one in the file and the easiest to skip. Don't skip it.
