#!/usr/bin/env python3
"""Generate README plots from hyperdemo's CSV output.

Run after `go run ./cmd/hyperdemo`. Reads docs/data/*.csv, writes
docs/img/*.png suitable for embedding in the README.
"""

from __future__ import annotations

import math
from pathlib import Path

import matplotlib.pyplot as plt
import pandas as pd

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / "docs" / "data"
OUT = ROOT / "docs" / "img"
OUT.mkdir(parents=True, exist_ok=True)

# Visual identity — clean, high contrast, GitHub light/dark friendly.
COLOR_BG = "#FFFFFF"
COLOR_FG = "#1F2328"
COLOR_GRID = "#D0D7DE"
COLOR_THEO = "#0969DA"   # blue
COLOR_EMP = "#CF222E"    # red
COLOR_KLL = "#1F883D"    # green
COLOR_TD = "#8250DF"     # purple
COLOR_HOT = "#BC4C00"    # orange
COLOR_TAIL = "#6E7781"   # grey

plt.rcParams.update({
    "figure.facecolor": COLOR_BG,
    "axes.facecolor": COLOR_BG,
    "axes.edgecolor": COLOR_FG,
    "axes.labelcolor": COLOR_FG,
    "axes.titlecolor": COLOR_FG,
    "xtick.color": COLOR_FG,
    "ytick.color": COLOR_FG,
    "text.color": COLOR_FG,
    "axes.spines.top": False,
    "axes.spines.right": False,
    "axes.grid": True,
    "grid.color": COLOR_GRID,
    "grid.linewidth": 0.6,
    "grid.alpha": 0.7,
    "font.family": "DejaVu Sans",
    "font.size": 11,
    "axes.titlesize": 13,
    "axes.titleweight": "bold",
    "axes.labelsize": 11,
    "legend.frameon": False,
})


def save(fig, name: str) -> None:
    out = OUT / name
    fig.savefig(out, dpi=160, bbox_inches="tight", facecolor=COLOR_BG)
    print(f"  wrote {out.relative_to(ROOT)}")
    plt.close(fig)


# ---------- Plot 1: HLL accuracy vs precision -------------------------------

def plot_hll_accuracy() -> None:
    df = pd.read_csv(DATA / "hll_accuracy.csv")
    df = df.sort_values("precision")

    fig, (ax_err, ax_mem) = plt.subplots(1, 2, figsize=(11, 4.2))

    ax_err.plot(df["precision"], df["theoretical_sigma"] * 100,
                "-", color=COLOR_THEO, lw=2.2,
                label="theoretical σ = 1.04/√m")
    ax_err.plot(df["precision"], df["empirical_rmse"] * 100,
                "o", color=COLOR_EMP, markersize=8,
                label="empirical RMSE (30 trials)")
    ax_err.set_yscale("log")
    ax_err.set_xlabel("precision parameter p (m = 2^p registers)")
    ax_err.set_ylabel("relative error (%, log scale)")
    ax_err.set_title("HyperLogLog: accuracy follows σ = 1.04/√m")
    ax_err.legend(loc="upper right")
    ax_err.set_xticks(df["precision"])

    ax_mem.bar(df["precision"], df["memory_bytes"] / 1024,
               color=COLOR_THEO, alpha=0.85, width=0.6)
    ax_mem.set_yscale("log")
    ax_mem.set_xlabel("precision parameter p")
    ax_mem.set_ylabel("memory (KiB, log scale)")
    ax_mem.set_title("HyperLogLog: memory grows as 2^p")
    ax_mem.set_xticks(df["precision"])
    for x, y in zip(df["precision"], df["memory_bytes"] / 1024):
        if y >= 1:
            label = f"{y:.0f} KiB"
        else:
            label = f"{int(y * 1024)} B"
        ax_mem.text(x, y * 1.15, label, ha="center", fontsize=9)

    fig.suptitle("hyperstats / hll", fontsize=10, color=COLOR_TAIL, y=1.02)
    save(fig, "hll_accuracy.png")


# ---------- Plot 2: HLL error vs cardinality (3-regime view) ----------------

def plot_hll_error_vs_n() -> None:
    df = pd.read_csv(DATA / "hll_error_vs_n.csv")
    df = df.sort_values("n")

    fig, ax = plt.subplots(figsize=(9, 4.5))
    ax.plot(df["n"], df["empirical_rmse"] * 100, "o-",
            color=COLOR_EMP, lw=2, markersize=7, label="empirical RMSE")
    ax.axhline(df["theoretical_sigma"].iloc[0] * 100,
               color=COLOR_THEO, lw=2, ls="--",
               label=f"asymptotic σ = {df['theoretical_sigma'].iloc[0]*100:.3f}%")

    # Annotate the three regimes from the docs.
    m = 2 ** 14
    ax.axvspan(1, 2 * m, alpha=0.08, color=COLOR_KLL,
               label="linear-counting regime (n ≤ 2m)")
    ax.axvspan(2 * m, 5 * m, alpha=0.10, color=COLOR_HOT,
               label="transition regime (2m < n ≤ 5m)")
    ax.axvspan(5 * m, df["n"].max() * 2, alpha=0.05, color=COLOR_THEO,
               label="asymptotic regime (n > 5m)")

    ax.set_xscale("log")
    ax.set_xlabel("true cardinality n (log scale)")
    ax.set_ylabel("relative error (%)")
    ax.set_title("HyperLogLog at p=14 — three accuracy regimes")
    ax.legend(loc="upper right", fontsize=9)
    ax.set_xlim(50, df["n"].max() * 2)
    save(fig, "hll_error_vs_n.png")


# ---------- Plot 3: CMS heavy-hitter fidelity -------------------------------

def plot_cms() -> None:
    df = pd.read_csv(DATA / "cms_heavy_hitters.csv")
    eps = 0.001
    N = df["true_count"].sum()  # not strictly N, but a proxy
    # Recompute ε·N bound from CMS doc: eps × total_mass.
    # We don't have total_mass in the CSV, so re-derive.
    bound = int(eps * 1_000_000)  # demo uses 1M events

    fig, (ax_hot, ax_dist) = plt.subplots(1, 2, figsize=(11.5, 4.5))

    hot = df[df["is_hot"]]
    tail = df[~df["is_hot"]]

    ax_hot.barh(range(len(hot)), hot["abs_error"],
                color=COLOR_HOT, alpha=0.9, label="absolute over-estimate")
    ax_hot.axvline(bound, color=COLOR_THEO, lw=2, ls="--",
                   label=f"εN bound = {bound}")
    ax_hot.set_yticks(range(len(hot)))
    ax_hot.set_yticklabels(hot["key"], fontsize=8)
    ax_hot.set_xlabel("absolute over-estimate (events)")
    ax_hot.set_title(f"Heavy-hitter accuracy (ε=0.1%, δ=1%)")
    ax_hot.legend(loc="lower right")

    # Right panel: error / true_count for hot vs tail.
    cats = ["heavy hitters\n(true ≈ 25k)", "tail keys\n(true < 20)"]
    means = [
        (hot["abs_error"] / hot["true_count"]).abs().mean() * 100,
        (tail["abs_error"] / tail["true_count"]).abs().mean() * 100,
    ]
    ax_dist.bar(cats, means, color=[COLOR_HOT, COLOR_TAIL], alpha=0.9)
    ax_dist.set_ylabel("mean relative error (%)")
    ax_dist.set_title("Heavy hitters: tight. Tail: εN dominates true count.")
    for x, y in zip(cats, means):
        ax_dist.text(x, y * 1.05, f"{y:.2f}%", ha="center", fontsize=10)

    save(fig, "cms_heavy_hitters.png")


# ---------- Plot 4: KLL vs t-digest rank error ------------------------------

def plot_quantile_comparison() -> None:
    df = pd.read_csv(DATA / "quantile_comparison.csv")
    df = df.sort_values("quantile").reset_index(drop=True)

    fig, ax = plt.subplots(figsize=(10, 4.8))
    x = range(len(df))
    ax.plot(x, df["kll_rank_err"] * 100, "o-",
            color=COLOR_KLL, lw=2, markersize=8,
            label="KLL (k=200)")
    ax.plot(x, df["tdigest_rank_err"] * 100, "s-",
            color=COLOR_TD, lw=2, markersize=8,
            label="t-digest (δ=100)")
    ax.set_yscale("log")
    ax.set_xlabel("quantile")
    ax.set_ylabel("rank error |R̂(q) − q| (%, log scale)")
    ax.set_title("KLL vs t-digest: rank error across quantiles (1M log-normal samples)")
    ax.legend(loc="upper left")
    ax.set_xticks(list(x))
    labels = []
    for q in df["quantile"]:
        if q < 0.99:
            labels.append(f"p{int(q * 100)}")
        elif q < 0.999:
            labels.append(f"p{q * 100:g}")
        else:
            labels.append(f"p{q * 100:.2f}".rstrip("0").rstrip("."))
    ax.set_xticklabels(labels)
    save(fig, "quantile_comparison.png")


# ---------- Plot 5: Throughput comparison -----------------------------------

def plot_throughput() -> None:
    df = pd.read_csv(DATA / "throughput.csv")
    df = df.sort_values("mops_per_sec", ascending=True)

    fig, ax = plt.subplots(figsize=(9, 3.8))
    bars = ax.barh(df["sketch"] + "  (" + df["config"] + ")",
                   df["mops_per_sec"],
                   color=[COLOR_THEO, COLOR_KLL, COLOR_TD, COLOR_HOT],
                   alpha=0.9)
    ax.set_xlabel("throughput (millions of operations / second, single-threaded)")
    ax.set_title("hyperstats throughput on a single core")
    for bar, v in zip(bars, df["mops_per_sec"]):
        ax.text(v + 0.3, bar.get_y() + bar.get_height() / 2,
                f"{v:.1f} Mops/s", va="center", fontsize=10)
    ax.set_xlim(0, df["mops_per_sec"].max() * 1.18)
    save(fig, "throughput.png")


# ---------- Plot 6: combined accuracy/memory landscape ----------------------

def plot_accuracy_memory_landscape() -> None:
    """Cross-sketch comparison: where each sketch lives in (memory, error) space."""
    fig, ax = plt.subplots(figsize=(8.5, 5.2))

    # HLL points.
    hll = pd.read_csv(DATA / "hll_accuracy.csv")
    ax.plot(hll["memory_bytes"] / 1024, hll["theoretical_sigma"] * 100,
            "o-", color=COLOR_THEO, lw=2, markersize=7, label="HyperLogLog")
    for _, row in hll.iterrows():
        ax.annotate(f"p={int(row['precision'])}",
                    (row["memory_bytes"] / 1024,
                     row["theoretical_sigma"] * 100),
                    textcoords="offset points", xytext=(7, 3),
                    fontsize=8, color=COLOR_THEO)

    # CMS hand-tabulated reference points (ε, δ, memory, ε as %).
    # memory = w × d × 8 bytes.
    cms_points = [
        # (eps, delta, w, d)
        (0.10, 0.01, 28, 5),
        (0.01, 0.01, 272, 5),
        (0.001, 0.01, 2719, 5),
        (0.0001, 0.01, 27183, 5),
    ]
    cms_x = [w * d * 8 / 1024 for _, _, w, d in cms_points]
    cms_y = [eps * 100 for eps, _, _, _ in cms_points]
    ax.plot(cms_x, cms_y, "s-", color=COLOR_HOT, lw=2, markersize=7,
            label="Count-Min (additive ε)")

    # KLL points.
    kll_points = [(100, 1.66 / 100), (200, 1.66 / 200), (800, 1.66 / 800)]
    kll_mem = [k * 3 * 8 / 1024 for k, _ in kll_points]
    kll_eps = [eps * 100 for _, eps in kll_points]
    ax.plot(kll_mem, kll_eps, "^-", color=COLOR_KLL, lw=2, markersize=7,
            label="KLL (per-query)")
    for (k, _), x, y in zip(kll_points, kll_mem, kll_eps):
        ax.annotate(f"k={k}", (x, y),
                    textcoords="offset points", xytext=(7, -10),
                    fontsize=8, color=COLOR_KLL)

    ax.set_xscale("log")
    ax.set_yscale("log")
    ax.set_xlabel("memory (KiB, log scale)")
    ax.set_ylabel("error (%, log scale)")
    ax.set_title("Sketch accuracy/memory landscape — where each sketch lives")
    ax.legend(loc="upper right")
    save(fig, "accuracy_memory_landscape.png")


def main() -> None:
    print("Generating plots from", DATA)
    plot_hll_accuracy()
    plot_hll_error_vs_n()
    plot_cms()
    plot_quantile_comparison()
    plot_throughput()
    plot_accuracy_memory_landscape()
    print("Done. Plots are in", OUT)


if __name__ == "__main__":
    main()
