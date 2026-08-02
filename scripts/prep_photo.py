#!/usr/bin/env python3
"""
Prep a photo for ASCII conversion:
1. Remove background with rembg (skipped if image already has alpha)
2. Boost local contrast with CLAHE
3. Composite onto white background
Output: source-prepped.png
"""

import io
import sys
import cv2
import numpy as np
from PIL import Image


def _remove_background(input_data: bytes) -> Image.Image:
    """Remove background via rembg when available; otherwise keep existing alpha."""
    img = Image.open(io.BytesIO(input_data)).convert("RGBA")
    alpha = np.array(img)[:, :, 3]
    # Already cut out (Facetune / PNG with transparency) — skip rembg
    if alpha.min() < 250:
        print("Existing alpha channel detected; skipping rembg.")
        return img

    try:
        from rembg import remove
    except ImportError as exc:
        raise SystemExit(
            "rembg is required for photos without transparency. "
            f"Install it or provide a transparent PNG. ({exc})"
        ) from exc

    print("Removing background...")
    return Image.open(io.BytesIO(remove(input_data))).convert("RGBA")


def prep_photo(input_path):
    """Prepare a photo for ASCII conversion."""

    print(f"Loading image: {input_path}")
    with open(input_path, "rb") as f:
        input_data = f.read()

    img_pil = _remove_background(input_data)
    img_arr = np.array(img_pil)
    img_cv = cv2.cvtColor(img_arr, cv2.COLOR_RGBA2BGR)
    alpha = img_arr[:, :, 3]

    gray = cv2.cvtColor(img_cv, cv2.COLOR_BGR2GRAY)

    print("Boosting local contrast...")
    clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))
    enhanced = clahe.apply(gray)

    white_bg = np.ones_like(enhanced) * 255
    alpha_norm = alpha.astype(float) / 255.0
    output = (
        enhanced.astype(float) * alpha_norm
        + white_bg.astype(float) * (1 - alpha_norm)
    ).astype(np.uint8)

    output_path = "source-prepped.png"
    cv2.imwrite(output_path, output)
    print(f"Saved to: {output_path}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python prep_photo.py <image_path>")
        sys.exit(1)

    prep_photo(sys.argv[1])
