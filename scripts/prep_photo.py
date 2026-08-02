"""
Prepare a portrait photo for clean ASCII conversion:
  1. isolate subject (use existing alpha, or rembg)
  2. crop to a bust and letterbox onto a white canvas (breathing room = clean ASCII)
  3. bilateral smooth + CLAHE local contrast
  4. soft bottom fade so dark clothing does not dominate the glyph field

Output: source-prepped.png (grayscale), consumed by make_ascii_svg.py.

    python scripts/prep_photo.py <input.png> [output.png]
"""
import os
import sys

import cv2
import numpy as np
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
INP = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "..", "photo.png")
OUT = sys.argv[2] if len(sys.argv) > 2 else os.path.join(HERE, "..", "source-prepped.png")

CANVAS = 460
# Keep head + upper torso; leave white margin so ASCII has clean empty space.
BUST_FRAC = 0.88          # fraction of subject height to keep from the top
TARGET_W_FRAC = 0.78      # subject width relative to canvas
TOP_MARGIN_FRAC = 0.07


def isolate(path: str) -> Image.Image:
    img = Image.open(path).convert("RGBA")
    alpha = np.array(img.split()[-1])
    if alpha.min() < 250:
        print("existing alpha detected; skipping rembg")
        return img
    try:
        from rembg import remove
    except ImportError as exc:
        raise SystemExit(
            "rembg required for photos without transparency. "
            f"Install it or provide a transparent PNG. ({exc})"
        ) from exc
    print("removing background with rembg...")
    return remove(img)


def bust_crop(img: Image.Image) -> Image.Image:
    alpha = np.array(img.split()[-1])
    ys, xs = np.where(alpha > 10)
    if len(xs) == 0:
        return img
    y0, y1 = int(ys.min()), int(ys.max())
    x0, x1 = int(xs.min()), int(xs.max())
    head_h = int((y1 - y0) * BUST_FRAC)
    y1 = y0 + head_h
    # slight horizontal pad inside the subject bbox
    pad_x = int((x1 - x0) * 0.03)
    x0 = max(0, x0 - pad_x)
    x1 = min(img.width, x1 + pad_x)
    return img.crop((x0, y0, x1, y1))


def letterbox(img: Image.Image, size: int = CANVAS) -> Image.Image:
    canvas = Image.new("RGBA", (size, size), (255, 255, 255, 0))
    tw = int(size * TARGET_W_FRAC)
    th = int(img.height * (tw / img.width))
    max_h = int(size * 0.86)
    if th > max_h:
        th = max_h
        tw = int(img.width * (th / img.height))
    resized = img.resize((tw, th), Image.LANCZOS)
    ox = (size - tw) // 2
    oy = int(size * TOP_MARGIN_FRAC)
    canvas.paste(resized, (ox, oy), resized)
    return canvas


cut = isolate(INP)
cut = bust_crop(cut)
framed = letterbox(cut)

rgb = np.array(framed.convert("RGB"))
alpha = np.array(framed.split()[-1]).astype(np.float32)

gray = cv2.cvtColor(rgb, cv2.COLOR_RGB2GRAY)
# Kill skin/jacket texture noise while keeping glasses/hair edges.
gray = cv2.bilateralFilter(gray, d=9, sigmaColor=45, sigmaSpace=45)
clahe = cv2.createCLAHE(clipLimit=2.6, tileGridSize=(8, 8))
gray = clahe.apply(gray)
gray = cv2.convertScaleAbs(gray, alpha=1.08, beta=20)

# Soft fade toward white in the lower jacket region.
h, w = gray.shape
yy = np.linspace(0, 1, h)[:, None]
fade = np.clip((yy - 0.72) / 0.22, 0, 1)
subject = gray.astype(np.float32) * (1.0 - 0.55 * fade) + 255.0 * (0.55 * fade)

mask = alpha / 255.0
mask = cv2.GaussianBlur(mask, (0, 0), 1.0)
out = np.clip(subject * mask + 255.0 * (1.0 - mask), 0, 255).astype(np.uint8)

# Darken strong edges (glasses, hair silhouette) so they map to denser glyphs.
edges = cv2.Canny(out, 60, 140)
edges = cv2.dilate(edges, np.ones((2, 2), np.uint8), iterations=1)
boosted = out.astype(np.int16)
boosted[edges > 0] = np.clip(boosted[edges > 0] - 55, 0, 255)
# Don't edge-boost pure white background
boosted[out > 248] = out[out > 248]
out = boosted.astype(np.uint8)

Image.fromarray(out, mode="L").save(OUT)
print("wrote", OUT, out.shape, "mean", round(float(out.mean()), 1),
      "white%", round(float((out > 240).mean()), 3))
