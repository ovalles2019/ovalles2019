#!/usr/bin/env python3
"""
Neofetch-style info card SVG — terminal chrome matching oscar-ascii.svg,
with staggered line-by-line fade/slide reveal (CSS keyframes inside the SVG).

Dimensions chosen so width=490 / height scales to match the ASCII portrait
when that portrait is shown at width=370 (same visual height).

    python scripts/make_info_card.py
    STATIC=1 python scripts/make_info_card.py   # frozen frame for Quick Look
"""
import html
import os

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "info-card.svg")
STATIC = bool(os.environ.get("STATIC"))

# Matches portrait display height when README uses width 370 / 490
W, H = 700, 575
PAD = 28
TITLEBAR_H = 30

BG = "#0d1117"
BG2 = "#111722"
FRAME = "#30363d"
MUTED = "#7d8590"
INK = "#c9d1d9"
LABEL = "#79c0ff"
ACCENT = "#58a6ff"
GREEN = "#3fb950"
PURPLE = "#a371f7"
GOLD = "#f2cc60"

# (label, value, accent_color)
ROWS = [
    ("Now", "Cloud & AI/ML Engineer", ACCENT),
    ("Focus", "Agentic workflows · RAG · MCP tooling", GREEN),
    ("Stack", "Python · AWS · LangChain · FastAPI · K8s", PURPLE),
    ("Certs", "AWS SAA-C03 · AWS Data Engineer · Anthropic", GOLD),
    ("School", "M.S. Computer Engineering · UTD", LABEL),
    ("Site", "oscar-valles.com", ACCENT),
]

LINE_DUR = 0.35
STAGGER = 0.12


def main():
    parts = []
    parts.append(
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
        f'viewBox="0 0 {W} {H}" font-family="ui-monospace, SFMono-Regular, '
        f'Menlo, Consolas, monospace">'
    )

    css = f"""
@keyframes slideIn {{
  from {{ opacity: 0; transform: translateX(-12px); }}
  to   {{ opacity: 1; transform: translateX(0); }}
}}
.row {{ opacity: 0; animation: slideIn {LINE_DUR:.2f}s ease-out forwards; }}
""".strip()
    if not STATIC:
        parts.append(f"<style>{css}</style>")

    parts.append(
        "<defs>"
        f'<linearGradient id="ibg" x1="0" y1="0" x2="0" y2="1">'
        f'<stop offset="0" stop-color="{BG2}"/><stop offset="1" stop-color="{BG}"/>'
        "</linearGradient></defs>"
    )
    parts.append(f'<rect width="{W}" height="{H}" rx="12" fill="url(#ibg)"/>')
    parts.append(
        f'<rect x="0.5" y="0.5" width="{W-1}" height="{H-1}" rx="12" '
        f'fill="none" stroke="{FRAME}" stroke-width="1"/>'
    )
    parts.append(f'<line x1="0" y1="{TITLEBAR_H}" x2="{W}" y2="{TITLEBAR_H}" stroke="{FRAME}"/>')
    for i, dotcol in enumerate(["#ff5f56", "#ffbd2e", "#27c93f"]):
        parts.append(f'<circle cx="{PAD + i*16}" cy="{TITLEBAR_H/2}" r="5" fill="{dotcol}"/>')
    parts.append(
        f'<text x="{W/2}" y="{TITLEBAR_H/2 + 4}" fill="{MUTED}" font-size="12" '
        f'text-anchor="middle">oscar@github: ~$ neofetch</text>'
    )

    # Header identity block
    y = TITLEBAR_H + 48
    delay = 0.0
    header = (
        f'<text x="{PAD}" y="{y}" fill="{INK}" font-size="22" font-weight="700">'
        f'Oscar Valles</text>'
    )
    sub = (
        f'<text x="{PAD}" y="{y + 28}" fill="{MUTED}" font-size="14">'
        f'Cloud &amp; AI/ML Engineer · Agentic systems</text>'
    )
    if STATIC:
        parts.append(header)
        parts.append(sub)
    else:
        parts.append(f'<g class="row" style="animation-delay:{delay:.2f}s">{header}</g>')
        delay += STAGGER
        parts.append(f'<g class="row" style="animation-delay:{delay:.2f}s">{sub}</g>')
        delay += STAGGER + 0.05

    parts.append(
        f'<line x1="{PAD}" y1="{y + 48}" x2="{W - PAD}" y2="{y + 48}" '
        f'stroke="{FRAME}" stroke-opacity="0.7"/>'
    )

    # Key/value rows
    row_y = y + 88
    label_w = 90
    for label, value, color in ROWS:
        lab = (
            f'<text x="{PAD}" y="{row_y}" fill="{color}" font-size="15" font-weight="700">'
            f'{html.escape(label)}</text>'
        )
        sep = f'<text x="{PAD + label_w - 18}" y="{row_y}" fill="{MUTED}" font-size="15">:</text>'
        val = (
            f'<text x="{PAD + label_w}" y="{row_y}" fill="{INK}" font-size="15">'
            f'{html.escape(value)}</text>'
        )
        group = lab + sep + val
        if STATIC:
            parts.append(group)
        else:
            parts.append(f'<g class="row" style="animation-delay:{delay:.2f}s">{group}</g>')
            delay += STAGGER
        row_y += 42

    # Status bar
    status_y = H - 22
    parts.append(f'<line x1="0" y1="{H - 36}" x2="{W}" y2="{H - 36}" stroke="{FRAME}"/>')
    parts.append(
        f'<text x="{PAD}" y="{status_y}" fill="{MUTED}" font-size="12">'
        f'oscar@github:~$ <tspan fill="{INK}">echo $STATUS</tspan>'
        f'<tspan fill="{GREEN}">  building agentic systems on AWS</tspan></text>'
    )
    parts.append(
        f'<rect x="{W - PAD - 10}" y="{status_y - 12}" width="8" height="14" fill="{INK}">'
        f'<animate attributeName="opacity" values="1;1;0;0" keyTimes="0;0.5;0.51;1" '
        f'dur="1s" repeatCount="indefinite"/></rect>'
    )

    parts.append("</svg>")
    svg = "".join(parts)
    with open(OUT, "w") as f:
        f.write(svg)
    print("wrote", OUT, len(svg), "bytes;", W, "x", H)


if __name__ == "__main__":
    main()
