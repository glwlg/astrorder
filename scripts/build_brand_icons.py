"""Resize the transparent brand master into web and Windows icon assets."""

from __future__ import annotations

from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
PUBLIC = ROOT / "frontend" / "public"
SOURCE = ROOT / "assets" / "brand" / "astrorder-mark.png"
DESKTOP = ROOT / "desktop-wails" / "assets"
ICON_SIZES = (16, 20, 24, 32, 40, 48, 64, 128, 256)
TRAY_SIZES = (16, 20, 24, 32, 40, 48, 64)


def resize(image: Image.Image, size: int) -> Image.Image:
    # Resample premultiplied colors so transparent pixels cannot bleed a matte
    # into the visible edge when the master is reduced to small Windows icons.
    return image.convert("RGBa").resize(
        (size, size), Image.Resampling.LANCZOS
    ).convert("RGBA")


def save_png(image: Image.Image, path: Path, size: int) -> None:
    resize(image, size).save(path, "PNG", optimize=True)


def save_ico(image: Image.Image, path: Path, sizes: tuple[int, ...]) -> None:
    frames = [resize(image, size) for size in sizes]
    frames[-1].save(
        path,
        format="ICO",
        sizes=[(size, size) for size in sizes],
        append_images=frames[:-1],
    )


def main() -> None:
    image = Image.open(SOURCE)
    if image.mode != "RGBA" or image.width != image.height:
        raise ValueError("brand master must be a square RGBA PNG")
    alpha = image.getchannel("A")
    if alpha.getextrema()[0] != 0 or alpha.getbbox() is None:
        raise ValueError("brand master must have a transparent background")
    corners = ((0, 0), (image.width - 1, 0), (0, image.height - 1), (image.width - 1, image.height - 1))
    if any(alpha.getpixel(point) != 0 for point in corners):
        raise ValueError("brand master corners must be fully transparent")
    PUBLIC.mkdir(parents=True, exist_ok=True)
    for name in ("logo.png", "logo-dark.png"):
        save_png(image, PUBLIC / name, 512)
    for size in (192, 512):
        for suffix in ("", "-dark"):
            save_png(image, PUBLIC / f"pwa-{size}{suffix}.png", size)
    for name in ("favicon.png", "favicon-dark.png"):
        save_png(image, PUBLIC / name, 64)
    save_png(image, PUBLIC / "apple-touch-icon.png", 180)
    save_ico(image, PUBLIC / "favicon.ico", ICON_SIZES)
    save_png(image, DESKTOP / "icon.png", 256)
    save_ico(image, DESKTOP / "app.ico", ICON_SIZES)
    for name in ("tray.ico", "tray-dark.ico"):
        save_ico(image, DESKTOP / name, TRAY_SIZES)
    print("wrote transparent brand assets to", PUBLIC, "and", DESKTOP)


if __name__ == "__main__":
    main()
