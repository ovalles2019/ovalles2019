#!/usr/bin/env python3
"""
Convert source-prepped.png to a self-typing ASCII art SVG.
Uses SMIL animations for row-by-row reveal with cursor effect.
"""

import cv2
import numpy as np
from PIL import Image

def image_to_ascii(img_path, width=100, height=53):
    """Convert image to ASCII characters."""
    
    # Load and resize image
    img = cv2.imread(img_path, cv2.IMREAD_GRAYSCALE)
    if img is None:
        raise FileNotFoundError(f"Image not found: {img_path}")
    
    # Calculate aspect ratio to maintain proportions
    aspect_ratio = img.shape[1] / img.shape[0]
    new_height = height
    new_width = int(new_height * aspect_ratio)
    
    if new_width > width:
        new_width = width
        new_height = int(new_width / aspect_ratio)
    
    img = cv2.resize(img, (new_width, new_height))
    
    # ASCII ramp: bright to dark
    RAMP = " .`:-=+*cs#%@"
    
    # Convert pixels to ASCII
    ascii_art = []
    for row in range(new_height):
        line = ""
        for col in range(new_width):
            pixel = img[row, col]
            # Bright pixels → sparse chars; dark pixels → dense chars
            normalized = pixel / 255.0
            char_idx = int((1.0 - normalized) * (len(RAMP) - 1))
            line += RAMP[char_idx]
        ascii_art.append(line)
    
    return ascii_art, new_width, new_height

def create_ascii_svg(ascii_art, width_chars, height_chars):
    """Create SVG with animated ASCII art."""
    
    # SVG dimensions (monospace font)
    char_width = 7.2
    char_height = 14.4
    svg_width = width_chars * char_width
    svg_height = height_chars * char_height
    
    # Start SVG
    svg_lines = [
        f'<svg viewBox="0 0 {svg_width} {svg_height}" xmlns="http://www.w3.org/2000/svg">',
        '<defs>',
        '<style>',
        '@font-face { font-family: "Courier New", monospace; }',
        'text { font-family: "Courier New", monospace; font-size: 12px; fill: #999999; }',
        '</style>',
        '</defs>',
    ]
    
    # Add animated text elements for each row
    for row_idx, line in enumerate(ascii_art):
        y = (row_idx + 1) * char_height - 2
        
        # Stagger animation: each row starts 50ms after the previous
        delay = row_idx * 50
        duration = 500  # Animation duration in ms
        total_delay = delay + duration + 1000  # Total time before it stops
        
        # Create clip path for cursor effect
        clip_id = f"clip-{row_idx}"
        svg_lines.append(f'<defs><clipPath id="{clip_id}">')
        svg_lines.append(f'<rect x="0" y="{y - char_height + 2}" width="{svg_width}" height="{char_height}"/>')
        svg_lines.append('</clipPath></defs>')
        
        # Text element with animation
        svg_lines.append(f'<text x="0" y="{y}" clip-path="url(#{clip_id})">')
        svg_lines.append(f'<animate attributeName="clip-path" values="url(#{clip_id})" dur="{duration}ms" begin="{delay}ms" fill="freeze"/>')
        svg_lines.append(line)
        svg_lines.append('</text>')
    
    # Simple version without complex animations (GitHub-safe)
    # Let's recreate with simpler approach
    svg_lines = [
        f'<svg viewBox="0 0 {svg_width} {svg_height}" xmlns="http://www.w3.org/2000/svg">',
        f'<rect width="{svg_width}" height="{svg_height}" fill="#0d1117"/>',
        '<defs>',
        '<style>',
        'text { font-family: "Courier New", monospace; font-size: 12px; fill: #c9d1d9; font-weight: normal; }',
        '.ascii-row { animation: fadeInUp 0.5s ease-out forwards; }',
        '@keyframes fadeInUp {',
        '  from { opacity: 0; transform: translateY(5px); }',
        '  to { opacity: 1; transform: translateY(0); }',
        '}',
        '</style>',
        '</defs>',
    ]
    
    # Add text elements
    for row_idx, line in enumerate(ascii_art):
        y = (row_idx + 1) * char_height - 2
        delay = row_idx * 50
        animation_delay = f"{delay}ms"
        svg_lines.append(
            f'<text x="0" y="{y}" class="ascii-row" style="animation-delay: {animation_delay}">{line}</text>'
        )
    
    svg_lines.append('</svg>')
    
    return "\n".join(svg_lines)

def main():
    print("Converting source-prepped.png to ASCII SVG...")
    
    ascii_art, width, height = image_to_ascii("source-prepped.png", width=100, height=53)
    
    svg_content = create_ascii_svg(ascii_art, width, height)
    
    with open("oscar-ascii.svg", "w") as f:
        f.write(svg_content)
    
    print(f"Created ASCII SVG: oscar-ascii.svg ({width}x{height} characters)")
    print(f"Total lines: {len(ascii_art)}")

if __name__ == "__main__":
    main()
