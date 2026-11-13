# Solaria brand kit

The mark, the wordmark, and the images for the GitHub organization, the repository and the X profile. The visual rules come from the website's design system in [`site/DESIGN.md`](../site/DESIGN.md) and its tokens in [`site/static/tokens.css`](../site/static/tokens.css). This kit applies them outside the site; when the two disagree, the design system wins and this kit is rebuilt.

## The idea, in one picture

Solaria is the planet in Asimov's robot novels where robots do the work and a few people set the direction. The mark is the product drawn small: the agent is a gold sun, and a host orbits it. Every larger image is the same picture zoomed out, with the real `reviewer` agent from `examples/reviewer` at the center and its three environments in orbit.

Gold still means one thing: the agent, and the host that changes. Silver is everything else.

## Files

Every SVG is self-contained, with text converted to outlines, so it renders the same in a browser, Figma or Inkscape with no fonts installed.

| File | Size | Use |
|---|---|---|
| `avatar/avatar-dark.png` | 1024×1024 | GitHub organization avatar, X avatar, any square profile picture. The default. |
| `avatar/avatar-dark-400.png` | 400×400 | The same, at X's recommended size. |
| `avatar/avatar-light.*` | 1024, 400 | Where a light square fits better, such as a light slide or a printed sheet. |
| `github/social-preview.png` | 1280×640 | Repository social preview: the card Slack, X and others show for a link to the repository. The build copies it to `site/static/`, where it's the site's `og:image`. |
| `github/banner-dark.png`, `banner-light.png` | 1600×400 | Top of the repository README and the organization profile, switched by color scheme. |
| `x/header.png` | 1500×500 | X profile header. |
| `logo/lockup-dark.svg`, `lockup-light.svg` | vector, plus a 1200px PNG | Mark and wordmark together, for slides, docs and partner pages. `dark` is for dark backgrounds. |
| `logo/mark-dark.svg`, `mark-light.svg` | vector | The mark alone. Same drawing as `site/static/favicon.svg`. |

`github/profile-README.md` and `x/profile.md` hold the text for each profile.

## The mark

Three parts on a 28-unit grid, the same as the site's bar:

- **Sun**: radius 7.5, filled with the gold sheen. This is the only place a gradient appears.
- **Orbit**: radius 12.5, a stroke of about 1 unit in `--muted`.
- **Host**: radius 2, at three o'clock on the orbit, in `--ink`.

Clear space around the mark is at least the sun's radius. Below 20px, draw the favicon instead: its orbit is 1.5 units so it survives at 16px.

Don't recolor the sun, rotate the mark, move the host off three o'clock, add a second host, outline the sun, or put the mark on a gold or photographic background.

## The wordmark

"Solaria" in Archivo at weight 640, width 116%, tracking -0.02em: the display style from the site. In the lockup the wordmark is 0.72 of the mark's height, separated by 0.36 of it. Use the lockup files instead of typing the name in a font.

## Color

| Role | Light | Dark |
|---|---|---|
| Ground | `#f1f2f4` | `#1a1e1d` |
| Ink | `#15171c` | `#e5e8e4` |
| Supporting text | `#3a3f48` | `#c3c9c5` |
| Lines and orbits | `#b4b9c1` | `#4c5651` |
| Gold | `#c99a2e` | `#d9ac45` |
| Gold text | `#7e5b0c` | `#e6be5e` |

The gold sheen runs at 135°: `#f3db8c` 0%, `#c99a2e` 46%, `#8f6a16` 54%, `#e9c766` 100%. The hard step in the middle is the fold of the metal; keep it.

Social images use the dark palette. X, GitHub's dark theme and most link previews sit on dark grounds, and graphite reads as a plate on both black and white.

## Type and copy

- Archivo for anything read, Martian Mono at 87.5% width for anything a terminal would print.
- Sentence case. No all-caps, no accent word in a headline.
- Lines come from the product: "Declare an agent once. Apply it wherever it can run." (the site), "Plan and apply agent definitions across hosts." (the README and `solaria --help`), "Agents do the work. People steer." (the site's footer).
- Console text in images is real output, copied from the site or a testscript. Don't edit it for a better screenshot.

## Rebuilding

The images are generated. Change `build.py`, not the SVGs, then run:

```bash
uv run brand/build.py
```

It needs [uv](https://docs.astral.sh/uv/) and `rsvg-convert` (`brew install librsvg`). The first run downloads Archivo and Martian Mono from [google/fonts](https://github.com/google/fonts) into `brand/.fonts/`, which git ignores. If the tokens in `site/static/tokens.css` change, copy them into `DARK` and `LIGHT` at the top of the script.

## Setting up the profiles

### GitHub organization (`solaria-sh`)

In the organization's **Settings → Profile**:

- **Profile picture**: `avatar/avatar-dark.png`.
- **Display name**: Solaria
- **Description**: Plan and apply AI agent definitions across hosts.
- **URL**: https://solaria.sh
- **Social accounts**: the X profile.

For the organization's profile page, create the public repository `solaria-sh/.github` and add [`github/profile-README.md`](github/profile-README.md) as `profile/README.md`. Pin `solaria` under the organization's **Pinned repositories**.

### Repository (`solaria-sh/solaria`)

- **About → Description**: Plan and apply AI agent definitions across hosts. A control plane for agents, Terraform-style.
- **About → Website**: https://solaria.sh
- **About → Topics**: `ai-agents`, `agents`, `control-plane`, `infrastructure-as-code`, `mcp`, `agent-skills`, `claude`, `cli`, `go`
- **Settings → General → Social preview**: upload `github/social-preview.png`.
- The README already shows `github/banner-*.png`.

### X

See [`x/profile.md`](x/profile.md) for the name, bio and a first post. Upload `avatar/avatar-dark-400.png` as the profile picture and `x/header.png` as the header. The header has no text, and its orbit sits in the middle: the web draws the avatar over the bottom left, and the phone app crops both sides and puts its status bar and buttons over the top and the middle of each side. `x_header` in `build.py` lists those areas.
