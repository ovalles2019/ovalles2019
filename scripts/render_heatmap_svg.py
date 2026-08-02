#!/usr/bin/env python3
"""
Render contribution heatmap SVG from data/contributions.json.
Animated reveal with diagonal slide effect.
"""

import json
from datetime import datetime, timedelta

def load_contributions():
    """Load contribution data from JSON."""
    try:
        with open("data/contributions.json", "r") as f:
            data = json.load(f)
        return data["contributions"], data["stats"]
    except FileNotFoundError:
        print("data/contributions.json not found. Run fetch_contributions.py first.")
        return {}, {}

def create_heatmap_svg(contributions, stats):
    """Generate heatmap SVG from contribution data."""
    
    # GitHub color palette: none -> brightest
    PALETTE = ["#161b22", "#0e4429", "#006d32", "#26a641", "#39d353", "#69f0a0"]
    
    # Calendar dimensions
    cell_size = 14
    cell_gap = 2
    weeks = 53
    days = 7
    
    # Margins for labels
    margin_left = 60
    margin_top = 40
    margin_bottom = 60
    margin_right = 20
    
    svg_width = margin_left + (weeks * (cell_size + cell_gap)) + margin_right
    svg_height = margin_top + (days * (cell_size + cell_gap)) + margin_bottom
    
    # Start SVG
    svg_lines = [
        f'<svg viewBox="0 0 {svg_width} {svg_height}" xmlns="http://www.w3.org/2000/svg">',
        '<defs>',
        '<style>',
        '.heatmap-cell { animation: popIn 0.3s ease-out forwards; opacity: 0; }',
        '@keyframes popIn {',
        '  from { opacity: 0; transform: scale(0.8); }',
        '  to { opacity: 1; transform: scale(1); }',
        '}',
        '.heatmap-label { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; font-size: 12px; fill: #8b949e; }',
        '.heatmap-title { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; font-size: 14px; font-weight: bold; fill: #c9d1d9; }',
        '.heatmap-stats { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; font-size: 11px; fill: #8b949e; }',
        '</style>',
        '</defs>',
        # Background (GitHub dark)
        f'<rect width="{svg_width}" height="{svg_height}" fill="#0d1117" rx="4"/>',
    ]
    
    # Title
    svg_lines.append(f'<text x="{margin_left}" y="25" class="heatmap-title">Contributions in the last year</text>')
    
    # Month labels (top)
    months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
    month_x = margin_left
    for i, month in enumerate(months):
        if i < 12:
            month_x = margin_left + (i * 4.4 * (cell_size + cell_gap))
            svg_lines.append(
                f'<text x="{month_x}" y="38" class="heatmap-label">{month}</text>'
            )
    
    # Day labels (left)
    day_labels = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
    for day_idx, label in enumerate(day_labels):
        y = margin_top + (day_idx * (cell_size + cell_gap)) + cell_size
        svg_lines.append(
            f'<text x="5" y="{y}" class="heatmap-label" text-anchor="start">{label[0]}</text>'
        )
    
    # Generate calendar grid
    today = datetime.now().date()
    year_ago = today - timedelta(days=365)
    
    current_date = year_ago
    week = 0
    day_of_week = current_date.weekday()
    if day_of_week != 6:  # Start from Sunday
        week = -1
    
    cell_idx = 0
    while current_date <= today:
        week = (current_date - year_ago).days // 7
        day = (current_date.weekday() + 1) % 7  # Convert to Sunday=0
        
        date_str = current_date.strftime("%Y-%m-%d")
        level = contributions.get(date_str, 0)
        
        x = margin_left + (week * (cell_size + cell_gap))
        y = margin_top + (day * (cell_size + cell_gap))
        
        color = PALETTE[level] if level < len(PALETTE) else PALETTE[-1]
        delay = cell_idx * 10  # Stagger animation
        
        # Add animated cell
        svg_lines.append(
            f'<rect x="{x}" y="{y}" width="{cell_size}" height="{cell_size}" '
            f'fill="{color}" rx="2" class="heatmap-cell" '
            f'style="animation-delay: {delay}ms" '
            f'data-date="{date_str}" data-count="{level}"/>'
        )
        
        current_date += timedelta(days=1)
        cell_idx += 1
    
    # Legend
    legend_y = svg_height - 35
    svg_lines.append(f'<text x="{margin_left}" y="{legend_y}" class="heatmap-stats">Less</text>')
    
    for i in range(len(PALETTE)):
        x = margin_left + 40 + (i * (cell_size + cell_gap))
        svg_lines.append(
            f'<rect x="{x}" y="{legend_y - 12}" width="{cell_size}" height="{cell_size}" '
            f'fill="{PALETTE[i]}" rx="2"/>'
        )
    
    svg_lines.append(f'<text x="{margin_left + 40 + (len(PALETTE) * (cell_size + cell_gap)) + 10}" y="{legend_y}" class="heatmap-stats">More</text>')
    
    # Stats footer
    total = stats.get("total_contributions", 0)
    streak = stats.get("current_streak", 0)
    svg_lines.append(
        f'<text x="{margin_left}" y="{svg_height - 10}" class="heatmap-stats">'
        f'{total:,} contributions in the last year | Current streak: {streak} days'
        f'</text>'
    )
    
    svg_lines.append('</svg>')
    
    return "\n".join(svg_lines)

def main():
    print("Rendering contribution heatmap...")
    
    contributions, stats = load_contributions()
    
    if not contributions:
        print("No contribution data found.")
        return
    
    svg_content = create_heatmap_svg(contributions, stats)
    
    with open("oscar-contrib-heatmap.svg", "w") as f:
        f.write(svg_content)
    
    print("Created heatmap SVG: oscar-contrib-heatmap.svg")
    print(f"Total contributions: {stats.get('total_contributions', 0)}")
    print(f"Current streak: {stats.get('current_streak', 0)} days")

if __name__ == "__main__":
    main()
