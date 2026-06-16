#!/usr/bin/env python3
"""Generate the Cortex .mcpb icon (mcpb/icon.png).

Original artwork - a connected-node graph on a diagonal indigo-to-cyan
gradient, evoking Cortex's memory graph synced across devices. No third-party
assets: it ships under the repo's MIT licence. Rendered at 4x and downscaled
with LANCZOS for crisp anti-aliasing.

Usage: python3 scripts/gen-icon.py [output_path]
Requires: pillow.
"""
from __future__ import annotations

import math
import os
import sys

from PIL import Image, ImageDraw

SIZE = 512
SS = 4  # supersample factor
S = SIZE * SS

GRAD_TL = (79, 70, 229)   # indigo  #4F46E5
GRAD_BR = (6, 182, 212)   # cyan    #06B6D4


def _lerp(a: tuple[int, int, int], b: tuple[int, int, int], t: float) -> tuple[int, int, int]:
    return tuple(round(a[i] + (b[i] - a[i]) * t) for i in range(3))


def _gradient(size: int) -> Image.Image:
    """Diagonal top-left -> bottom-right gradient."""
    img = Image.new("RGB", (size, size))
    px = img.load()
    denom = 2 * (size - 1)
    for y in range(size):
        for x in range(size):
            px[x, y] = _lerp(GRAD_TL, GRAD_BR, (x + y) / denom)
    return img


def _rounded_mask(size: int, radius: int) -> Image.Image:
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, size - 1, size - 1], radius=radius, fill=255)
    return mask


def build() -> Image.Image:
    base = _gradient(S).convert("RGBA")
    base.putalpha(_rounded_mask(S, radius=int(0.20 * S)))

    overlay = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(overlay)

    cx = cy = S / 2
    ring_r = 0.295 * S
    edge_w = max(1, int(0.013 * S))
    hub_r = 0.060 * S
    node_r = 0.040 * S

    # Six ring nodes (pointy-top hexagon).
    pts = [
        (cx + ring_r * math.cos(math.radians(a)), cy + ring_r * math.sin(math.radians(a)))
        for a in range(-90, 270, 60)
    ]

    line = (255, 255, 255, 205)
    # Spokes hub -> ring, then the hexagon outline.
    for x, y in pts:
        d.line([(cx, cy), (x, y)], fill=line, width=edge_w)
    for i, (x, y) in enumerate(pts):
        nx, ny = pts[(i + 1) % len(pts)]
        d.line([(x, y), (nx, ny)], fill=line, width=edge_w)

    def node(x: float, y: float, r: float) -> None:
        glow = r * 1.55
        d.ellipse([x - glow, y - glow, x + glow, y + glow], fill=(255, 255, 255, 60))
        d.ellipse([x - r, y - r, x + r, y + r], fill=(255, 255, 255, 255))

    for x, y in pts:
        node(x, y, node_r)
    node(cx, cy, hub_r)

    base.alpha_composite(overlay)
    return base.resize((SIZE, SIZE), Image.LANCZOS)


def main() -> None:
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "mcpb", "icon.png"
    )
    os.makedirs(os.path.dirname(out), exist_ok=True)
    build().save(out, "PNG")
    print(f"gen-icon: wrote {out}")


if __name__ == "__main__":
    main()
