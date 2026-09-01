"""Anomaly scoring over a window of device readings.

This is the platform's only CPU-bound workload, and it is what makes the
autoscaling story real. Scaling a stock web server on CPU produces a graph that
looks like autoscaling without demonstrating anything: the load generator is the
only thing that moved. Here, request cost is a function of window length, so
saturation, the HPA reaction and the recovery are all properties of the service
rather than of the test harness.

The detector is a robust z-score (median / MAD) rather than mean / standard
deviation. Mean and standard deviation are both computed from the very outliers
being looked for, so a single large spike inflates the standard deviation and
masks itself -- the classic reason a naive z-score detector goes quiet exactly
when something has gone wrong.
"""

from __future__ import annotations

import math
from collections.abc import Sequence
from dataclasses import dataclass

# 0.6745 is the 0.75 quantile of the standard normal distribution. Dividing the
# MAD by it rescales the estimator so that, for normally distributed data, it
# matches the standard deviation -- which is what keeps the usual "3 sigma"
# intuition applicable to the threshold.
_MAD_TO_SIGMA = 0.6745

# Below this many points the dispersion estimate is too unstable to call an
# anomaly, so the service reports "not enough evidence" instead of guessing.
MIN_WINDOW = 8

MAX_WINDOW = 4096


class ScoringError(ValueError):
    """Raised when a window cannot be scored."""


@dataclass(frozen=True)
class ScoreResult:
    """The outcome of scoring one window."""

    score: float
    anomaly: bool
    method: str
    window: int
    median: float
    dispersion: float

    def as_dict(self) -> dict:
        return {
            "score": self.score,
            "anomaly": self.anomaly,
            "method": self.method,
            "window": self.window,
            "median": self.median,
            "dispersion": self.dispersion,
        }


def _median(values: Sequence[float]) -> float:
    ordered = sorted(values)
    n = len(ordered)
    mid = n // 2
    if n % 2 == 1:
        return ordered[mid]
    return (ordered[mid - 1] + ordered[mid]) / 2.0


def score_window(readings: Sequence[float], threshold: float = 3.5) -> ScoreResult:
    """Score the most recent reading against the window that precedes it.

    Returns a robust z-score for the final element, and whether it exceeds
    ``threshold``. The score is capped rather than allowed to reach infinity so
    that a zero-dispersion window produces a usable number instead of a value
    that breaks JSON encoding and every downstream aggregation.
    """
    if not readings:
        raise ScoringError("readings must not be empty")
    if len(readings) > MAX_WINDOW:
        raise ScoringError(f"readings must contain at most {MAX_WINDOW} values")

    for value in readings:
        # NaN and infinity survive JSON parsing in most clients and would
        # silently poison the median, so they are rejected at the boundary.
        if not math.isfinite(value):
            raise ScoringError("readings must all be finite numbers")

    window = len(readings)
    if window < MIN_WINDOW:
        # Honest under-determination beats a confident wrong answer.
        return ScoreResult(
            score=0.0,
            anomaly=False,
            method="insufficient_data",
            window=window,
            median=float(_median(readings)),
            dispersion=0.0,
        )

    baseline = readings[:-1]
    latest = readings[-1]

    median = _median(baseline)
    absolute_deviations = [abs(value - median) for value in baseline]
    mad = _median(absolute_deviations)

    if mad == 0.0:
        # A perfectly flat baseline has no scale to measure against. Any change
        # at all is notable, but the magnitude is not meaningful, so the score
        # is reported as a saturated constant rather than a division by zero.
        deviation = abs(latest - median)
        if deviation == 0.0:
            return ScoreResult(0.0, False, "robust_zscore_flat", window, median, 0.0)
        return ScoreResult(
            score=float(_SATURATED_SCORE),
            anomaly=True,
            method="robust_zscore_flat",
            window=window,
            median=median,
            dispersion=0.0,
        )

    sigma = mad / _MAD_TO_SIGMA
    z = abs(latest - median) / sigma
    score = min(z, _SATURATED_SCORE)

    return ScoreResult(
        score=float(score),
        anomaly=bool(z >= threshold),
        method="robust_zscore",
        window=window,
        median=float(median),
        dispersion=float(sigma),
    )


# Caps the reported score. Callers store this in a float column and plot it;
# an unbounded value makes every chart useless the first time it appears.
_SATURATED_SCORE = 1000.0
