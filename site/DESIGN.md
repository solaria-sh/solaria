# Solaria design system

The contract every Solaria page follows. Tokens live in [`tokens.css`](tokens.css); this file explains them and the rules around them. When a page needs something this file doesn't cover, add it here first.

## The idea

In Asimov's robot novels, Solaria is a planet where robots do nearly all the work and the few people there set the direction. The brand carries three ideas from that:

- **Robots work, people steer.** The product is a control plane. Pages show the machine doing the work (a plan, an apply, a digest) and leave the decisions to the reader.
- **Order.** Like Asimov's Three Laws, Solaria's guarantees are explicit: plans don't surprise you, an agent keeps one identity, and a host never receives what it can't run.
- **Sol.** The agent sits at the center like a sun, and its hosts orbit around it.

The feel is calm, precise and industrial: a spec sheet or a machine plate, not a startup launch.

## Color: gold and silver

Flat colors can't be metallic, so the metals become roles instead.

- **Silver is every neutral.** Light mode uses cool greys; dark mode uses graphite. The two modes have equal weight, and the page follows the system setting unless the reader picks one.
- **Gold is the one signal, and it always means "this acts or changes".** That covers the apply button, an update (`~`) in a plan, the agent at the center, and the open host in the orbit. Gold is never decoration.
- **Plan colors come from the product:** `--create` (+), `--update` (~, which shares the gold text tone), `--delete` (-).
- **Metal sheens** (`--sheen-gold`, `--sheen-silver`) appear only in the mark and the orbit's sun.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--ground` | `#f1f2f4` | `#1a1e1d` | Page |
| `--surface` | `#ffffff` | `#232826` | Plates, cards |
| `--sunk` | `#e6e8eb` | `#151817` | Console, code |
| `--line` / `--line-strong` | `#d3d6dc` / `#b4b9c1` | `#37403c` / `#4c5651` | Rules, borders |
| `--ink` / `--ink-2` / `--muted` | `#15171c` / `#3a3f48` / `#555b66` | `#e5e8e4` / `#c3c9c5` / `#9ba39f` | Text, from primary to supporting |
| `--gold` | `#c99a2e` | `#d9ac45` | Gold fills, with `--on-gold` text |
| `--gold-ink` | `#7e5b0c` | `#e6be5e` | Gold text and digests |
| `--create` / `--delete` | `#256b43` / `#b5352c` | `#74c493` / `#ef8173` | Plan output |

Every text pairing passes WCAG AA in both modes. Recheck contrast when you add a pairing.

## Type

- **Archivo** for everything you read. Display text runs wide (`font-stretch: 116%`, weight 640, tracking -0.02em), like stamped machine plates. Body text is regular width.
- **Martian Mono** at 87.5% width, only for what a terminal or a card file would show: commands, plan output, YAML, digests, environment names.
- Sentence case everywhere. No all-caps eyebrows, no labels above headings unless they say something the heading doesn't.
- No accent word in a headline. The whole line carries the idea.

## Motion

Mechanical and sparse. Things latch into place; nothing floats, fades up on scroll, or bounces.

- `--ease-latch` (ease out, no overshoot) for things that appear or change state, over `--t-latch` (180 ms).
- `--ease-travel` for things that move, over `--t-travel` (420 ms).
- The orbit advances in discrete ticks, like an escapement. It pauses while a host is open and while it's off screen.
- Every animation shows the real product: real commands, real output, real digests from `examples/`.
- With `prefers-reduced-motion`, every piece renders its final state.

## Shape and layout

- Corners stay small: `--radius` (3px) for controls, `--radius-plate` (6px) for plates.
- Hairline rules divide sections. Shadows appear only on floating cards.
- Let the content set the structure: a real table for providers, a real console for the workflow. No grids of three identical cards, no bento, no stat strips.
- Pages must work at 375px wide with no horizontal scroll. Long plan lines wrap inside the console. Tables collapse to labeled rows.

## Copy

- Say what the product does, in the README's voice. No "seamless", "unlock", "simply" or "just".
- Never invent a claim, a number or an output. Copy CLI output from a real run or a testscript.
- Buttons start with a verb: "Install Solaria", "Read the concepts".

## Avoid

These patterns read as generated, and none of them fits Solaria: purple or blue-violet gradients, cream with terracotta, near-black with one neon accent, emerald as the fallback accent, Inter or Space Grotesk, gradient text, glass cards, fake window chrome with traffic-light dots, `01 / 02 / 03` markers on content that isn't a sequence, count-up numbers, and fade-up on every section. The `avoid-ai-design` skill in `.claude/skills/` has the full catalog and a scanner.

## Files

- `main.go`: the generator. It renders `templates/` and every page under `docs/` into `public/`, rewriting relative Markdown links into site URLs, or into GitHub URLs for files the site doesn't publish. `RepoURL` in it is the only place the repository address is written.
- `templates/`: `base.html` (head, bar, footer), `home.html` (the landing page), `doc.html` (sidebar, article, outline), `404.html` (served by Cloudflare Pages for any path without a file; without it Pages would answer with the home page).
- `static/`: copied as is. `tokens.css` holds every color, type, motion and space value; `site.css` and `docs.css` use only those tokens; `site.js` runs the motion, the theme switch, the install tabs and the docs menu.
- `static/install.sh` and `static/install.ps1`: served at `solaria.sh/install.sh` and `solaria.sh/install.ps1`. They download a release from GitHub, check its SHA-256 against `checksums.txt` and install without root.
- `static/_headers`: Cloudflare Pages response headers. It serves the install scripts as plain text with a short cache, and keeps the `pages.dev` copies out of search results.
- `explore/`: the exploration page from the design rounds. It isn't published.

`make site-serve` builds and serves the site on http://localhost:4173, rebuilding on each page load. `make site-test` builds it and checks that every internal link and anchor resolves. `make site` writes `site/public/` for deployment.
