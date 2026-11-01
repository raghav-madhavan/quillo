"""Generate Quillo's placeholder brand images.

Run from the repository root: python3 scripts/quillo-brand/generate.py
Replace the outputs with real artwork when it exists.
"""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[2]
FONT = "/System/Library/Fonts/HelveticaNeue.ttc"
ACCENT = (124, 92, 255)  # violet
ACCENT_DARK = (72, 44, 196)


def font(size):
    try:
        return ImageFont.truetype(FONT, size, index=1)  # bold
    except OSError:
        return ImageFont.load_default(size)


def mark(size, padding=0.0, rounded=True):
    """A violet rounded tile with a white quill-stroke "Q"."""
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    inset = int(size * padding)
    box = (inset, inset, size - inset - 1, size - inset - 1)
    radius = int((size - 2 * inset) * 0.23) if rounded else 0
    # vertical gradient
    grad = Image.new("RGBA", (size, size))
    gd = ImageDraw.Draw(grad)
    for y in range(size):
        t = y / max(size - 1, 1)
        c = tuple(int(ACCENT[i] * (1 - t) + ACCENT_DARK[i] * t) for i in range(3)) + (255,)
        gd.line([(0, y), (size, y)], fill=c)
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle(box, radius=radius, fill=255)
    img.paste(grad, (0, 0), mask)

    inner = size - 2 * inset
    cx, cy = size / 2, size / 2 - inner * 0.02
    r = inner * 0.26
    w = max(int(inner * 0.085), 2)
    d.ellipse((cx - r, cy - r, cx + r, cy + r), outline="white", width=w)
    # quill tail of the Q
    d.line(
        [(cx + r * 0.2, cy + r * 0.2), (cx + r * 1.0, cy + r * 1.0)],
        fill="white",
        width=w,
    )
    return img


def wordmark(width, height):
    """White "Quillo" text for dark surfaces, with the mark on the left."""
    img = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    m = mark(int(height * 0.62))
    img.paste(m, (0, (height - m.height) // 2), m)
    f = font(int(height * 0.42))
    d = ImageDraw.Draw(img)
    x = m.width + int(height * 0.12)
    bbox = d.textbbox((0, 0), "Quillo", font=f)
    y = (height - (bbox[3] - bbox[1])) // 2 - bbox[1]
    d.text((x, y), "Quillo", font=f, fill="white")
    return img


def main():
    public = ROOT / "web" / "public"
    mark(1024).save(public / "quillo-icon-1024.png")
    mark(512).save(public / "web-app-icon-512.png")
    mark(192).save(public / "web-app-icon-192.png")
    mark(512, padding=0.1, rounded=False).save(public / "maskable-icon-512.png")
    # iOS adds its own rounding to touch icons
    mark(180, rounded=False).convert("RGB").save(public / "apple-touch-icon.png")
    mark(256).save(public / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)])
    mark(256).save(ROOT / "assets" / "icon.png")
    wm = wordmark(191, 100)
    wm.save(public / "quillo-wordmark-sidebar.png")
    wm.save(ROOT / "internal" / "mail" / "assets" / "quillo-wordmark.png")


if __name__ == "__main__":
    main()
