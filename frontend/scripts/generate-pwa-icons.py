"""Rasterize the existing favicon geometry; requires Pillow for asset generation only."""
from pathlib import Path
from PIL import Image, ImageDraw

root = Path(__file__).resolve().parents[1] / 'public'
for size in (192, 512):
    scale = size * 3 / 48
    image = Image.new('RGB', (size * 3, size * 3), '#1d2740')
    draw = ImageDraw.Draw(image)
    def points(values):
        return [(x * scale, y * scale) for x, y in values]
    draw.line(points([(12, 32), (24, 14), (36, 34)]), fill='#aebeff', width=round(1.5 * scale))
    for x, y, color in [(12, 32, '#74d8c1'), (24, 14, '#f3cb78'), (36, 34, '#aebeff')]:
        draw.ellipse(((x - 3) * scale, (y - 3) * scale, (x + 3) * scale, (y + 3) * scale), fill=color)
    draw.polygon(points([(24, 21), (25.5, 24.2), (29, 24.6), (26.4, 27), (27.1, 30.5), (24, 28.7), (20.9, 30.5), (21.6, 27), (19, 24.6), (22.5, 24.2)]), fill='white')
    image.resize((size, size), Image.Resampling.LANCZOS).save(root / f'pwa-{size}.png', optimize=True)
    print(f'pwa-{size}.png generated')
