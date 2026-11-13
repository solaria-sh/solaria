# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "uharfbuzz>=0.40"]
# ///
"""Build the Solaria brand kit: SVG sources and PNG exports.

    uv run brand/build.py

Colors and type come from site/static/tokens.css and site/DESIGN.md. Text is
set with the real fonts (Archivo, Martian Mono) through HarfBuzz and written
as outlines, so every SVG renders the same anywhere, with no fonts installed.
The fonts are downloaded once from github.com/google/fonts into brand/.fonts.
PNGs are rendered with rsvg-convert (brew install librsvg).
"""

from __future__ import annotations

import math
import shutil
import subprocess
import sys
import urllib.request
from dataclasses import dataclass
from pathlib import Path

import uharfbuzz as hb
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen

ROOT = Path(__file__).resolve().parent
FONTS = ROOT / ".fonts"
FONT_URLS = {
    "Archivo.ttf": "https://raw.githubusercontent.com/google/fonts/main/ofl/archivo/Archivo%5Bwdth%2Cwght%5D.ttf",
    "MartianMono.ttf": "https://raw.githubusercontent.com/google/fonts/main/ofl/martianmono/MartianMono%5Bwdth%2Cwght%5D.ttf",
}

# Tokens, copied from site/static/tokens.css. Keep them in sync.
DARK = dict(
    ground="#1a1e1d", surface="#232826", sunk="#151817", line="#37403c",
    line_strong="#4c5651", ink="#e5e8e4", ink2="#c3c9c5", muted="#9ba39f",
    gold="#d9ac45", gold_ink="#e6be5e", create="#74c493",
)
LIGHT = dict(
    ground="#f1f2f4", surface="#ffffff", sunk="#e6e8eb", line="#d3d6dc",
    line_strong="#b4b9c1", ink="#15171c", ink2="#3a3f48", muted="#555b66",
    gold="#c99a2e", gold_ink="#7e5b0c", create="#256b43",
)
SUN_INK, SUN_INK_2 = "#1a1407", "#3b2c07"  # .sun-t and .sun-s in site.css

SHEEN = (
    '<linearGradient id="sheen" x1="0" y1="0" x2="1" y2="1">'
    '<stop offset="0" stop-color="#f3db8c"/><stop offset=".46" stop-color="#c99a2e"/>'
    '<stop offset=".54" stop-color="#8f6a16"/><stop offset="1" stop-color="#e9c766"/>'
    "</linearGradient>"
)


# Type ----------------------------------------------------------------------


@dataclass(frozen=True)
class Face:
    file: str
    wdth: float
    wght: float
    track: float = 0.0  # em


DISPLAY = Face("Archivo.ttf", 116, 640, -0.02)  # --display-*
SANS = Face("Archivo.ttf", 100, 400)
SUN = Face("Archivo.ttf", 110, 700)
MONO = Face("MartianMono.ttf", 87.5, 400)  # --mono-stretch
MONO_BOLD = Face("MartianMono.ttf", 87.5, 600)

_fonts: dict[Face, tuple[hb.Font, int]] = {}


def _font(face: Face) -> tuple[hb.Font, int]:
    if face not in _fonts:
        path = FONTS / face.file
        if not path.exists():
            FONTS.mkdir(exist_ok=True)
            print(f"download {face.file}")
            urllib.request.urlretrieve(FONT_URLS[face.file], path)
        hbface = hb.Face(hb.Blob.from_file_path(str(path)))
        font = hb.Font(hbface)
        font.set_variations({"wdth": face.wdth, "wght": face.wght})
        _fonts[face] = (font, hbface.upem)
    return _fonts[face]


def _shape(text: str, face: Face):
    font, upem = _font(face)
    buf = hb.Buffer()
    buf.add_str(text)
    buf.guess_segment_properties()
    hb.shape(font, buf, {"kern": True, "liga": True})
    return font, upem, buf.glyph_infos, buf.glyph_positions


def measure(text: str, face: Face, size: float) -> float:
    _, upem, infos, pos = _shape(text, face)
    adv = sum(p.x_advance for p in pos) + face.track * upem * max(len(infos) - 1, 0)
    return adv * size / upem


def text(s: str, face: Face, size: float, x: float, y: float, fill: str, anchor: str = "start") -> str:
    """Return an outlined <path> for s, with its baseline at y."""
    font, upem, infos, pos = _shape(s, face)
    if anchor != "start":
        w = measure(s, face, size)
        x -= w if anchor == "end" else w / 2
    k = size / upem
    pen = SVGPathPen(None, ntos=lambda v: f"{v:.2f}".rstrip("0").rstrip("."))
    cx = 0.0
    for info, p in zip(infos, pos):
        t = TransformPen(pen, (k, 0, 0, -k, x + (cx + p.x_offset) * k, y - p.y_offset * k))
        font.draw_glyph_with_pen(info.codepoint, t)
        cx += p.x_advance + face.track * upem
    return f'<path fill="{fill}" d="{pen.getCommands()}"/>'


# Pieces --------------------------------------------------------------------


def mark(cx: float, cy: float, u: float, t: dict, ring: float = 1.0) -> str:
    """The mark: a gold sun, one orbit, one host. u is the size of one unit
    of the 28-unit mark in site/templates/base.html (sun 7.5, orbit 12.5,
    host 2), centered on cx, cy."""
    return (
        f'<circle cx="{cx}" cy="{cy}" r="{7.5 * u:.2f}" fill="url(#sheen)"/>'
        f'<circle cx="{cx}" cy="{cy}" r="{12.5 * u:.2f}" fill="none" stroke="{t["muted"]}" stroke-width="{ring * u:.2f}"/>'
        f'<circle cx="{cx + 12.5 * u:.2f}" cy="{cy}" r="{2 * u:.2f}" fill="{t["ink"]}"/>'
    )


def lockup(x: float, cy: float, h: float, t: dict, ring: float = 1.0) -> tuple[str, float]:
    """Mark and wordmark side by side, as in the site's bar. h is the mark's
    height; the wordmark's cap height is about 0.48 h. Returns (svg, width)."""
    u = h / 28
    size = h * 0.72
    gap = h * 0.36
    svg = mark(x + 14 * u, cy, u, t, ring)
    tx = x + 28 * u + gap
    svg += text("Solaria", DISPLAY, size, tx, cy + size * 0.36, t["ink"])
    return svg, 28 * u + gap + measure("Solaria", DISPLAY, size)


@dataclass
class Host:
    name: str
    ring: int  # index into rings
    angle: float  # degrees, clockwise from three o'clock
    side: str = "e"  # label east or west of the node
    open: bool = False


def orrery(cx, cy, sun_r, rings, dial, hosts, t, label_size=15, sun_label=True, clip=None) -> str:
    """The orbit from the landing page: a sun, rings, a ticked dial ring and
    hosts with their environment names."""
    out = []
    if dial is not None:
        r = rings[dial]
        for i in range(120):
            a = math.radians(i * 3)
            major = i % 10 == 0
            ln = 14 if major else 6
            out.append(
                f'<line x1="{cx + r * math.cos(a):.2f}" y1="{cy + r * math.sin(a):.2f}" '
                f'x2="{cx + (r - ln) * math.cos(a):.2f}" y2="{cy + (r - ln) * math.sin(a):.2f}" '
                f'stroke="{t["line_strong"] if major else t["line"]}" stroke-width="{1.5 if major else 1.25}"/>'
            )
    for r in rings:
        out.append(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="{t["line_strong"]}" stroke-width="1.25"/>')
    out.append(f'<circle cx="{cx}" cy="{cy}" r="{sun_r}" fill="url(#sheen)"/>')
    if sun_label:
        out.append(text("reviewer", SUN, sun_r * 0.27, cx, cy - sun_r * 0.02, SUN_INK, "middle"))
        out.append(text("sonnet-5-5", MONO, sun_r * 0.165, cx, cy + sun_r * 0.27, SUN_INK_2, "middle"))
    for h in hosts:
        r = rings[h.ring]
        a = math.radians(h.angle)
        x, y = cx + r * math.cos(a), cy + r * math.sin(a)
        w = measure(h.name, MONO, label_size)
        pad = 7
        lx = x + 16 if h.side == "e" else x - 16 - w
        out.append(
            f'<rect x="{lx - pad:.2f}" y="{y - label_size * 0.95:.2f}" width="{w + 2 * pad:.2f}" '
            f'height="{label_size * 1.9:.2f}" rx="3" fill="{t["ground"]}" '
            f'stroke="{t["gold"] if h.open else t["line_strong"]}" stroke-width="1.25"/>'
        )
        out.append(text(h.name, MONO, label_size, lx, y + label_size * 0.36, t["gold_ink"] if h.open else t["ink2"]))
        fill, stroke = (t["gold"], t["gold"]) if h.open else (t["ground"], t["ink2"])
        out.append(f'<circle cx="{x:.2f}" cy="{y:.2f}" r="6.5" fill="{fill}" stroke="{stroke}" stroke-width="2"/>')
    return "".join(out)


def svg(w: int, h: int, body: str, title: str) -> str:
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" '
        f'role="img" aria-label="{title}"><title>{title}</title><defs>{SHEEN}</defs>{body}</svg>\n'
    )


# Assets --------------------------------------------------------------------


def avatar(t: dict) -> str:
    # Safe inside the circle crop X and other sites use: the host stays
    # inside a circle of radius 0.44 of the side.
    n = 1024
    u = n * 0.44 / 14.5
    body = f'<rect width="{n}" height="{n}" fill="{t["ground"]}"/>' + mark(n / 2, n / 2, u, t, ring=0.9)
    return svg(n, n, body, "Solaria")


def mark_only(t: dict) -> str:
    u = 1.0
    return svg(32, 32, mark(16, 16, u, t, ring=1.5), "Solaria")


def lockup_svg(t: dict) -> str:
    h = 56
    pad = 4
    piece, w = lockup(pad, pad + h / 2, h, t, ring=1.3)
    return svg(math.ceil(w + 2 * pad), h + 2 * pad, piece, "Solaria")


SOCIAL_CONSOLE = [
    ("cmd", "solaria plan -e ma-cloud"),
    ("muted", "agent.yaml: warning: limits aren't enforced on Managed Agents"),
    ("ink", "Environment ma-cloud (managed-agents/cloud)"),
    ("ink", ""),
    ("create", "  + Environment/ma-cloud"),
    ("ink", ""),
    ("create", "  + Agent/reviewer"),
    ("digest", "      resolvedDigest: sha256:ff171fe9c371"),
    ("ink", ""),
    ("muted", "Plan: 2 to create, 0 to update, 0 to replace, 0 to delete or archive."),
]


def console(x, y, w, lines, t, size=17, lh=28, pad=30, height=None) -> str:
    """A console plate with real plan output, as on the landing page."""
    h = height or pad * 2 + lh * len(lines)
    out = [f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="10" fill="{t["sunk"]}" stroke="{t["line"]}" stroke-width="1.5"/>']
    by = y + pad + size
    for kind, s in lines:
        tx = x + pad + 2
        if kind == "cmd":
            out.append(text("$", MONO, size, tx, by, t["muted"]))
            tx += measure("$ ", MONO, size)
            out.append(text(s, MONO, size, tx, by, t["ink"]))
        elif kind == "digest":
            k, v = s.split(": ")
            out.append(text(k + ": ", MONO, size, tx, by, t["ink"]))
            out.append(text(v, MONO, size, tx + measure(k + ": ", MONO, size), by, t["gold_ink"]))
        elif s:
            out.append(text(s, MONO, size, tx, by, t[kind]))
        by += lh
    return "".join(out)


def social_preview(t: dict) -> str:
    # GitHub's repository social preview: 1280x640, keep 40pt clear.
    W, H, m = 1280, 640, 72
    body = [f'<rect width="{W}" height="{H}" fill="{t["ground"]}"/>']
    piece, _ = lockup(m, 92, 44, t, ring=1.3)
    body.append(piece)
    body.append(text("solaria.sh", MONO, 19, W - m, 99, t["muted"], "end"))
    lines = ["Declare an agent once.", "Apply it wherever it can run."]
    size = min(62, (W - 2 * m) / max(measure(s, DISPLAY, 1) for s in lines))
    body.append(text(lines[0], DISPLAY, size, m, 218, t["ink"]))
    body.append(text(lines[1], DISPLAY, size, m, 218 + size * 1.12, t["ink"]))
    body.append(console(m, 334, W - 2 * m, SOCIAL_CONSOLE, t, size=16.5, lh=27, pad=28, height=H))
    return svg(W, H, "".join(body), "Solaria: declare an agent once, apply it wherever it can run")


HOSTS = [
    Host("ma-cloud", 0, -128, "w", open=True),
    Host("ma-self-hosted", 1, 142, "w"),
    Host("local-gitagent", 2, 32, "w"),
]


def x_header(t: dict) -> str:
    # X profile header: 1500x500. The avatar covers the bottom left
    # (about x < 380, y > 330) and phones crop the top and bottom edges.
    W, H = 1500, 500
    body = [f'<rect width="{W}" height="{H}" fill="{t["ground"]}"/>']
    body.append(orrery(1150, 250, 86, [154, 232, 318, 410], 3, HOSTS, t))
    size = 58
    body.append(text("Agents do the work.", DISPLAY, size, 96, 190, t["ink"]))
    body.append(text("People steer.", DISPLAY, size, 96, 190 + size * 1.14, t["ink"]))
    return svg(W, H, "".join(body), "Solaria: agents do the work, people steer")


BANNER_HOSTS = [
    Host("ma-cloud", 0, -150, "w", open=True),
    Host("ma-self-hosted", 1, 160, "w"),
    Host("local-gitagent", 2, -156, "w"),
]


def readme_banner(t: dict) -> str:
    # README header. GitHub shows it at about 830px wide, so 1600x400 is 2x.
    W, H = 1600, 400
    body = [
        f'<clipPath id="plate"><rect width="{W}" height="{H}" rx="14"/></clipPath>',
        f'<g clip-path="url(#plate)"><rect width="{W}" height="{H}" fill="{t["ground"]}"/>',
        orrery(1500, 214, 92, [168, 254, 344, 440], 3, BANNER_HOSTS, t, label_size=16),
    ]
    piece, _ = lockup(92, 170, 104, t, ring=1.2)
    body.append(piece)
    body.append(text("Plan and apply agent definitions across hosts.", SANS, 30, 96, 290, t["ink2"]))
    body.append("</g>")
    return svg(W, H, "".join(body), "Solaria: plan and apply agent definitions across hosts")


# Output --------------------------------------------------------------------

ASSETS = {
    # path: (svg, [(png width, png suffix)])
    "logo/mark-dark.svg": (lambda: mark_only(DARK), []),
    "logo/mark-light.svg": (lambda: mark_only(LIGHT), []),
    "logo/lockup-dark.svg": (lambda: lockup_svg(DARK), [(1200, "")]),
    "logo/lockup-light.svg": (lambda: lockup_svg(LIGHT), [(1200, "")]),
    "avatar/avatar-dark.svg": (lambda: avatar(DARK), [(1024, ""), (400, "-400")]),
    "avatar/avatar-light.svg": (lambda: avatar(LIGHT), [(1024, ""), (400, "-400")]),
    "github/social-preview.svg": (lambda: social_preview(DARK), [(1280, "")]),
    "github/banner-dark.svg": (lambda: readme_banner(DARK), [(1600, "")]),
    "github/banner-light.svg": (lambda: readme_banner(LIGHT), [(1600, "")]),
    "x/header.svg": (lambda: x_header(DARK), [(1500, "")]),
}


def main() -> int:
    rsvg = shutil.which("rsvg-convert")
    if not rsvg:
        print("rsvg-convert not found: brew install librsvg", file=sys.stderr)
        return 1
    for rel, (make, pngs) in ASSETS.items():
        out = ROOT / rel
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(make())
        print(rel)
        for width, suffix in pngs:
            png = out.with_name(out.stem + suffix + ".png")
            subprocess.run([rsvg, "-w", str(width), "-o", str(png), str(out)], check=True)
            print(png.relative_to(ROOT))
    # The site serves the social preview as its og:image (site/main.go).
    site_card = ROOT.parent / "site/static/social-preview.png"
    shutil.copyfile(ROOT / "github/social-preview.png", site_card)
    print(site_card.relative_to(ROOT.parent))
    return 0


if __name__ == "__main__":
    sys.exit(main())
