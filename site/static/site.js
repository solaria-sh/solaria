// Solaria landing page. Motion is mechanical and sparse: things latch into
// place, the orbit advances in discrete ticks, nothing bounces. With reduced
// motion every piece renders its final state.
(() => {
  "use strict";

  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const sleep = ms => new Promise(r => setTimeout(r, reduce ? 0 : ms));
  const root = document.documentElement;
  const onView = (el, fn, threshold = 0.35) => {
    if (!("IntersectionObserver" in window)) return fn();
    const io = new IntersectionObserver(es => {
      if (es.some(e => e.isIntersecting)) { io.disconnect(); fn(); }
    }, { threshold });
    io.observe(el);
  };

  /* Theme: auto, light, dark. */
  const themeBtn = document.getElementById("theme");
  const themeLabel = document.getElementById("themeLabel");
  const themes = ["auto", "light", "dark"];
  const names = { auto: "Auto", light: "Light", dark: "Dark" };
  let theme = root.dataset.theme || "auto";
  const showTheme = () => {
    themeLabel.textContent = names[theme];
    themeBtn.setAttribute("aria-label", `Color theme: ${theme === "auto" ? "follows system" : theme}`);
  };
  showTheme();
  themeBtn.addEventListener("click", () => {
    theme = themes[(themes.indexOf(theme) + 1) % themes.length];
    if (theme === "auto") delete root.dataset.theme; else root.dataset.theme = theme;
    try { theme === "auto" ? localStorage.removeItem("solaria-theme") : localStorage.setItem("solaria-theme", theme); } catch (e) {}
    showTheme();
  });

  /* First screen: the real reviewer plan for ma-cloud, then apply. */
  const con = document.querySelector("#console code");
  const hosts = [...document.querySelectorAll("#hosts .host")];
  const digests = {
    "ma-cloud": ["applied", "sha256:ff171fe9c371"],
    "ma-self-hosted": ["same card", "sha256:ff171fe9c371"],
    "local-gitagent": ["Haiku override", "sha256:3b017ab6c64e"],
  };
  const script = [
    { cmd: "solaria plan -e ma-cloud" },
    { k: "w", t: "agent.yaml: warning: limits aren't enforced on Managed Agents" },
    { t: "Environment ma-cloud (managed-agents/cloud)" },
    { t: "" },
    { k: "c", t: "  + Environment/ma-cloud" },
    { t: "" },
    { k: "c", t: "  + Agent/reviewer" },
    { h: '      resolvedDigest: <span class="dg">sha256:ff171fe9c371</span>' },
    { t: "" },
    { k: "m", t: "Plan: 2 to create, 0 to update, 0 to replace, 0 to delete or archive." },
    { cmd: "solaria apply -e ma-cloud --auto-approve", pause: 700 },
    { t: "" },
    { t: "Apply complete: 2 created, 0 updated, 0 replaced, 0 deleted. State serial 1.", hosts: true },
  ];
  const line = (cls) => { const s = document.createElement("span"); s.className = "ln" + (cls ? " " + cls : ""); con.appendChild(s); return s; };
  const setHost = h => {
    const [what, dig] = digests[h.dataset.env];
    h.classList.add("on");
    h.querySelector(".dig").textContent = `${what} · ${dig.slice(7)}`;
  };
  async function runConsole() {
    con.textContent = "";
    for (const s of script) {
      if (s.cmd) {
        await sleep(s.pause || 300);
        const ln = line("cmd");
        if (reduce) { ln.textContent = s.cmd; continue; }
        const caret = document.createElement("span"); caret.className = "caret";
        for (const ch of s.cmd) { ln.textContent += ch; ln.appendChild(caret); await sleep(26); }
        await sleep(240); caret.remove();
      } else {
        await sleep(s.t === "" ? 30 : 100);
        const ln = line(s.k);
        if (s.h) ln.innerHTML = s.h; else ln.textContent = s.t;
        if (!reduce) ln.classList.add("latch");
      }
      if (s.hosts) {
        for (const h of hosts) { await sleep(h === hosts[0] ? 240 : 520); setHost(h); }
      }
    }
  }
  // On wide screens the console is in the first screen, so start right away;
  // on narrow ones it sits below the fold, so wait until it's seen.
  if (con) {
    if (matchMedia("(min-width: 900px)").matches) (document.fonts ? document.fonts.ready : Promise.resolve()).then(runConsole);
    else onView(con, runConsole, 0.2);
  }

  /* Orbit: reviewer at the center, three hosts as small nodes that open. */
  const envs = [
    { name: "local-gitagent", r: 128, a: 205, step: 15, moved: true,
      facts: [["Provider", "gitagent / docker"], ["Agent loop", "gitagent, in a container image"], ["Tools run in", "The same container"], ["Model", "claude-haiku-5-5 (override)"], ["Digest", "sha256:3b017ab6c64e", true]],
      note: "The override changes this environment only, so only this digest moves." },
    { name: "ma-self-hosted", r: 188, a: 320, step: 10,
      facts: [["Provider", "managed-agents / self-hosted"], ["Agent loop", "Claude Managed Agents"], ["Tools run in", "A worker container on your machine"], ["Model", "claude-sonnet-5-5"], ["Digest", "sha256:ff171fe9c371", true]],
      note: "Same definition as ma-cloud, so the same digest." },
    { name: "ma-cloud", r: 246, a: 62, step: 5,
      facts: [["Provider", "managed-agents / cloud"], ["Agent loop", "Claude Managed Agents"], ["Tools run in", "Anthropic's sandbox"], ["Model", "claude-sonnet-5-5"], ["Digest", "sha256:ff171fe9c371", true]],
      note: "Apply creates the environment and the agent on Anthropic's side." },
  ];
  const orbit = document.getElementById("orbit");
  const nodesEl = document.getElementById("nodes");
  const detail = document.getElementById("orbitDetail");
  const ticks = document.getElementById("ticks");
  const narrow = matchMedia("(max-width: 640px)");

  if (orbit) {
    for (let i = 0; i < 72; i++) {
      const g = i * 5 * Math.PI / 180, r1 = 252, r2 = i % 6 ? 256 : 262;
      const l = document.createElementNS("http://www.w3.org/2000/svg", "line");
      l.setAttribute("class", "tick");
      l.setAttribute("x1", (260 + r1 * Math.cos(g)).toFixed(1)); l.setAttribute("y1", (260 + r1 * Math.sin(g)).toFixed(1));
      l.setAttribute("x2", (260 + r2 * Math.cos(g)).toFixed(1)); l.setAttribute("y2", (260 + r2 * Math.sin(g)).toFixed(1));
      ticks.appendChild(l);
    }

    const factsHTML = e => `<p class="facts-title">${e.name}</p><dl class="facts">${e.facts.map(([k, v, d]) => `<dt>${k}</dt><dd${d ? ' class="dg"' : ""}>${v}</dd>`).join("")}</dl><p class="facts-note">${e.note}</p>`;
    const nodes = envs.map((e, i) => {
      const n = document.createElement("div");
      n.className = "node" + (e.moved ? " moved" : "");
      n.innerHTML = `<button class="node-btn" type="button" aria-expanded="false" aria-controls="nc${i}"><span>${e.name}</span></button><div class="node-card" id="nc${i}" role="region" aria-label="${e.name}">${factsHTML(e)}</div>`;
      nodesEl.appendChild(n);
      return n;
    });

    const state = { angles: envs.map(e => e.a), shown: envs.map(e => e.a), open: -1, hover: false, visible: false, raf: 0 };

    const draw = () => {
      nodes.forEach((n, i) => {
        const a = state.shown[i] * Math.PI / 180, r = envs[i].r;
        const x = (260 + r * Math.cos(a)) / 520, y = (260 + r * Math.sin(a)) / 520;
        n.style.left = (x * 100).toFixed(3) + "%";
        n.style.top = (y * 100).toFixed(3) + "%";
        // open the card toward the center so it stays inside the orbit
        n.classList.toggle("flip", x > 0.6);
        const card = n.lastElementChild;
        card.classList.toggle("e", x < 0.5); card.classList.toggle("w", x >= 0.5);
        card.classList.toggle("s", y < 0.5); card.classList.toggle("n", y >= 0.5);
      });
    };
    const tickTo = () => {
      cancelAnimationFrame(state.raf);
      const from = state.shown.slice(), t0 = performance.now(), dur = reduce ? 0 : 420;
      const ease = t => 1 - Math.pow(1 - t, 4);
      const frame = now => {
        const t = dur ? Math.min(1, (now - t0) / dur) : 1;
        state.shown = from.map((a, i) => a + (state.angles[i] - a) * ease(t));
        draw();
        if (t < 1) state.raf = requestAnimationFrame(frame);
      };
      state.raf = requestAnimationFrame(frame);
    };
    draw();

    const showDetail = i => {
      if (i < 0) { detail.innerHTML = '<p class="facts-note" style="margin:0">Tap a host to see what it changes.</p>'; detail.classList.remove("on"); return; }
      detail.innerHTML = factsHTML(envs[i]); detail.classList.add("on");
    };
    const setOpen = i => {
      state.open = i;
      nodes.forEach((n, j) => {
        n.classList.toggle("open", j === i);
        n.firstElementChild.setAttribute("aria-expanded", String(j === i));
      });
      showDetail(i);
    };
    showDetail(-1);

    nodes.forEach((n, i) => {
      const btn = n.firstElementChild;
      btn.addEventListener("click", () => setOpen(state.open === i && !narrow.matches ? -1 : i));
      n.addEventListener("mouseenter", () => { if (!narrow.matches) { state.hover = true; setOpen(i); } });
      n.addEventListener("mouseleave", () => { if (!narrow.matches) { state.hover = false; setOpen(-1); } });
    });
    document.addEventListener("keydown", e => { if (e.key === "Escape" && state.open >= 0) { const b = nodes[state.open].firstElementChild; setOpen(-1); b.focus(); } });
    document.addEventListener("click", e => { if (!narrow.matches && state.open >= 0 && !e.target.closest(".node")) setOpen(-1); });

    if (!reduce && "IntersectionObserver" in window) {
      new IntersectionObserver(es => { state.visible = es[0].isIntersecting; }, { threshold: 0.2 }).observe(orbit);
      setInterval(() => {
        // pause while someone is reading a host, or while nobody can see it
        if (!state.visible || document.hidden || state.hover || (state.open >= 0 && !narrow.matches)) return;
        state.angles = state.angles.map((a, i) => a + envs[i].step);
        tickTo();
      }, 1600);
    }
  }

  /* Laws: set one at a time when they come into view. */
  const laws = document.getElementById("laws");
  if (laws && !reduce) {
    laws.classList.add("armed");
    onView(laws, async () => {
      for (const l of laws.querySelectorAll(".law")) {
        l.classList.add("shown"); await sleep(380);
        l.classList.add("set"); await sleep(320);
      }
    }, 0.3);
  }

  /* Install tabs: start on the visitor's system. Without JS every panel shows. */
  const tabs = [...document.querySelectorAll("#osTabs .tab")];
  if (tabs.length) {
    const select = (tab, focus) => {
      tabs.forEach(t => {
        const on = t === tab;
        t.setAttribute("aria-selected", String(on));
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
      });
      if (focus) tab.focus();
    };
    const ua = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || navigator.userAgent;
    const os = /win/i.test(ua) ? "windows" : /linux|android|x11/i.test(ua) ? "linux" : "macos";
    select(tabs.find(t => t.dataset.os === os) || tabs[0], false);
    tabs.forEach((t, i) => {
      t.addEventListener("click", () => select(t, false));
      t.addEventListener("keydown", e => {
        const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
        if (d) { e.preventDefault(); select(tabs[(i + d + tabs.length) % tabs.length], true); }
      });
    });
  }

  /* Docs sidebar: a closed menu on phones, always open on wide screens. */
  const side = document.getElementById("sideMenu");
  if (side) {
    const wide = matchMedia("(min-width: 900px)");
    const fit = () => { side.open = wide.matches; };
    fit();
    wide.addEventListener("change", fit);
  }

  /* Doc tables: label each cell with its column, so phones can stack rows. */
  document.querySelectorAll(".prose table").forEach(t => {
    const head = [...t.querySelectorAll("thead th")].map(th => th.textContent.trim());
    t.querySelectorAll("tbody tr").forEach(tr => [...tr.children].forEach((td, i) => { td.dataset.label = head[i] || ""; }));
  });

  /* Copy buttons. */
  document.querySelectorAll("[data-copy]").forEach(b => b.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(b.dataset.copy); b.textContent = "Copied"; }
    catch (e) { b.textContent = "Select it"; }
    setTimeout(() => { b.textContent = "Copy"; }, 1600);
  }));
})();
