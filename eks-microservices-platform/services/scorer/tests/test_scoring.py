"""Tests for the detector itself, independent of HTTP."""

from __future__ import annotations

import math

import pytest

from app.scoring import MAX_WINDOW, MIN_WINDOW, ScoringError, score_window


def steady(n: int = 40, value: float = 10.0) -> list[float]:
    """A baseline with realistic but small variation."""
    return [value + (i % 3) * 0.1 for i in range(n)]


def test_steady_window_is_not_anomalous() -> None:
    result = score_window(steady())
    assert not result.anomaly
    assert result.method == "robust_zscore"
    assert result.score < 3.5


def test_clear_spike_is_flagged() -> None:
    readings = steady() + [500.0]
    result = score_window(readings)
    assert result.anomaly, f"a 50x spike scored only {result.score}"
    assert result.score > 3.5


def test_masking_spike_does_not_hide_a_second_one() -> None:
    """The reason this detector uses median/MAD rather than mean/stddev.

    A mean-and-standard-deviation detector computes its dispersion from the very
    outliers it is looking for. One huge spike in the baseline inflates the
    standard deviation so much that a later genuine anomaly falls inside the
    threshold and is never reported -- the detector goes quiet exactly when
    something is wrong. A robust estimator is unmoved by it.
    """
    baseline = steady(40)
    baseline[5] = 10_000.0  # a single contaminating outlier in the history
    readings = baseline + [500.0]  # a real anomaly now

    result = score_window(readings)
    assert result.anomaly, (
        "the contaminated baseline masked a genuine anomaly; the dispersion estimate is not robust"
    )

    # Show the failure concretely: the naive estimator misses it.
    history = readings[:-1]
    mean = sum(history) / len(history)
    variance = sum((x - mean) ** 2 for x in history) / len(history)
    naive_z = abs(readings[-1] - mean) / math.sqrt(variance)
    assert naive_z < 3.5, "the naive detector was expected to miss this case"


def test_short_window_reports_insufficient_data() -> None:
    result = score_window([1.0, 2.0, 3.0])
    assert result.method == "insufficient_data"
    assert not result.anomaly
    assert result.score == 0.0


def test_min_window_boundary() -> None:
    assert score_window(steady(MIN_WINDOW - 1)).method == "insufficient_data"
    assert score_window(steady(MIN_WINDOW)).method == "robust_zscore"


def test_flat_window_with_no_change_is_normal() -> None:
    result = score_window([5.0] * 20)
    assert not result.anomaly
    assert result.score == 0.0
    assert result.dispersion == 0.0


def test_flat_window_with_a_change_is_anomalous_and_finite() -> None:
    """Zero dispersion must not produce infinity.

    An unbounded score breaks JSON encoding, and any dashboard or aggregate
    downstream, the first time a perfectly flat sensor moves.
    """
    result = score_window([5.0] * 20 + [6.0])
    assert result.anomaly
    assert math.isfinite(result.score)
    assert result.method == "robust_zscore_flat"


def test_scores_are_always_finite() -> None:
    for readings in (steady(), steady() + [1e18], [0.0] * 30 + [1e-9]):
        result = score_window(readings)
        assert math.isfinite(result.score), f"non-finite score for {readings[:3]}..."
        assert math.isfinite(result.dispersion)


def test_rejects_empty_window() -> None:
    with pytest.raises(ScoringError):
        score_window([])


def test_rejects_oversized_window() -> None:
    with pytest.raises(ScoringError):
        score_window([1.0] * (MAX_WINDOW + 1))


@pytest.mark.parametrize("bad", [float("nan"), float("inf"), float("-inf")])
def test_rejects_non_finite_readings(bad: float) -> None:
    with pytest.raises(ScoringError):
        score_window(steady() + [bad])


def test_threshold_is_respected() -> None:
    readings = steady() + [11.0]
    strict = score_window(readings, threshold=0.5)
    lenient = score_window(readings, threshold=100.0)
    assert strict.anomaly
    assert not lenient.anomaly
    assert strict.score == lenient.score, "the threshold must not change the score itself"


def test_negative_deviation_is_detected() -> None:
    """A drop is as much an anomaly as a spike."""
    result = score_window(steady() + [-500.0])
    assert result.anomaly


def test_cost_grows_with_window_size() -> None:
    """The property the HPA depends on.

    If request cost were flat, CPU-based autoscaling here would be measuring the
    load generator rather than the service.
    """
    import time

    def elapsed(n: int) -> float:
        readings = steady(n)
        start = time.perf_counter()
        for _ in range(20):
            score_window(readings)
        return time.perf_counter() - start

    small = elapsed(64)
    large = elapsed(2048)
    assert large > small * 4, (
        f"scoring 2048 points took {large:.4f}s vs {small:.4f}s for 64; "
        "request cost is not tracking window size"
    )
