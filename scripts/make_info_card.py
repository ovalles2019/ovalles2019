#!/usr/bin/env python3
"""
Create a neofetch-style info card SVG showing role, current focus, stack, and highlights.
"""

import os

def create_info_card_svg():
    """Generate the info card SVG."""
    
    # Card dimensions
    card_width = 480
    card_height = 320
    padding = 20
    row_height = 40
    label_width = 120
    
    # Colors: GitHub dark theme
    bg_color = "#0d1117"
    border_color = "#30363d"
    title_color = "#58a6ff"
    label_color = "#79c0ff"
    value_color = "#c9d1d9"
    accent_colors = {
        "Now": "#79c0ff",      # Blue
        "Stack": "#a371f7",    # Purple
        "Highlights": "#56d364", # Green
    }
    
    svg_lines = [
        f'<svg viewBox="0 0 {card_width} {card_height}" xmlns="http://www.w3.org/2000/svg">',
        '<defs>',
        '<style>',
        f'text {{ font-family: "Courier New", monospace; fill: {value_color}; font-size: 13px; }}',
        f'.title {{ fill: {title_color}; font-weight: bold; font-size: 14px; }}',
        f'.label {{ fill: {label_color}; font-weight: bold; }}',
        '.row { animation: slideInLeft 0.4s ease-out forwards; opacity: 0; }',
        '@keyframes slideInLeft {',
        '  from { opacity: 0; transform: translateX(-10px); }',
        '  to { opacity: 1; transform: translateX(0); }',
        '}',
        '</style>',
        '</defs>',
        # Background
        f'<rect width="{card_width}" height="{card_height}" fill="{bg_color}" stroke="{border_color}" stroke-width="1" rx="4"/>',
        # Title bar
        f'<rect width="{card_width}" height="35" fill="{border_color}" rx="4"/>',
        f'<text x="10" y="25" class="title">oscar@github</text>',
    ]
    
    # Card content
    content = [
        ("Now", "Cloud/AI Engineer • Agentic Workflows"),
        ("Focus", "RAG • LLM Governance • AWS DevOps"),
        ("Stack", "Python • AWS • LangChain • FastAPI"),
        ("Certs", "AWS SAA • AWS Data Engineer • Anthropic"),
    ]
    
    y_offset = 55
    for idx, (label, value) in enumerate(content):
        delay = idx * 100
        svg_lines.append(
            f'<text x="{padding}" y="{y_offset}" class="label row" style="animation-delay: {delay}ms">{label}:</text>'
        )
        # Wrap value if it's long
        words = value.split(" • ")
        for word_idx, word in enumerate(words):
            word_y = y_offset + (word_idx * 15)
            svg_lines.append(
                f'<text x="{padding + label_width}" y="{word_y}" class="row" style="animation-delay: {delay + 50}ms">{word}</text>'
            )
        y_offset += 55
    
    # Footer stats
    svg_lines.append(f'<line x1="{padding}" y1="{card_height - 35}" x2="{card_width - padding}" y2="{card_height - 35}" stroke="{border_color}" stroke-width="1"/>')
    svg_lines.append(f'<text x="{padding}" y="{card_height - 15}" style="font-size: 11px; fill: {border_color};">portfolio: oscar-valles.com | AWS SAA-C03 | Anthropic Certified</text>')
    
    svg_lines.append('</svg>')
    
    return "\n".join(svg_lines)

def main():
    print("Generating neofetch-style info card SVG...")
    
    svg_content = create_info_card_svg()
    
    with open("oscar-info-card.svg", "w") as f:
        f.write(svg_content)
    
    print("Created info card SVG: oscar-info-card.svg")

if __name__ == "__main__":
    main()
