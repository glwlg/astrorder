"""Crop source logos and emit favicon / PWA / in-app brand assets."""

from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageOps

ROOT = Path(__file__).resolve().parent.parent
PUBLIC = ROOT / "frontend" / "public"
LIGHT_SRC = ROOT / "newlogo.png"
DARK_SRC = ROOT / "newlogo-dark.png"


def content_bbox(image: Image.Image, *, dark: bool) -> tuple[int, int, int, int]:
    gray = ImageOps.grayscale(image)
    mask = gray.point(lambda value: 255 if value > 18 else 0) if dark else gray.point(lambda value: 255 if value < 237 else 0)
    box = mask.getbbox()
    if box is None:
        raise ValueError("logo has no visible mark")
    return box


def union_bbox(*boxes: tuple[int, int, int, int]) -> tuple[int, int, int, int]:
    return (
        min(box[0] for box in boxes),
        min(box[1] for box in boxes),
        max(box[2] for box in boxes),
        max(box[3] for box in boxes),
    )


def crop_square(image: Image.Image, box: tuple[int, int, int, int], pad_ratio: float) -> Image.Image:
    x0, y0, x1, y1 = box
    side = max(x1 - x0, y1 - y0)
    pad = int(round(side * pad_ratio))
    cx = (x0 + x1) / 2
    cy = (y0 + y1) / 2
    half = side / 2 + pad
    left = int(round(cx - half))
    top = int(round(cy - half))
    size = max(int(round(half * 2)), 1)
    background = image.getpixel((0, 0))
    canvas = Image.new("RGB", (size, size), background)
    canvas.paste(image, (-left, -top))
    return canvas


def save_png(image: Image.Image, path: Path, size: int) -> None:
    image.resize((size, size), Image.Resampling.LANCZOS).save(path, "PNG", optimize=True)


def save_ico(image: Image.Image, path: Path) -> None:
    image.save(path, format="ICO", sizes=[(16, 16), (32, 32), (48, 48)])


def main() -> None:
    light = Image.open(LIGHT_SRC).convert("RGB")
    dark = Image.open(DARK_SRC).convert("RGB")
    box = union_bbox(content_bbox(light, dark=False), content_bbox(dark, dark=True))
    ui_light = crop_square(light, box, 0.08)
    ui_dark = crop_square(dark, box, 0.08)
    pwa_light = crop_square(light, box, 0.14)
    pwa_dark = crop_square(dark, box, 0.14)

    PUBLIC.mkdir(parents=True, exist_ok=True)
    save_png(ui_light, PUBLIC / "logo.png", 512)
    save_png(ui_dark, PUBLIC / "logo-dark.png", 512)
    save_png(pwa_light, PUBLIC / "pwa-192.png", 192)
    save_png(pwa_light, PUBLIC / "pwa-512.png", 512)
    save_png(pwa_dark, PUBLIC / "pwa-192-dark.png", 192)
    save_png(pwa_dark, PUBLIC / "pwa-512-dark.png", 512)
    save_png(ui_light, PUBLIC / "favicon.png", 64)
    save_png(ui_dark, PUBLIC / "favicon-dark.png", 64)
    save_png(pwa_light, PUBLIC / "apple-touch-icon.png", 180)
    save_ico(ui_light, PUBLIC / "favicon.ico")
    print("wrote brand icons to", PUBLIC)


if __name__ == "__main__":
    main()
