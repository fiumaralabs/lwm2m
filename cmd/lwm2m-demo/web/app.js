"use strict";
// LwM2M demo page. No framework, no external requests. Every string from
// the server (endpoint names, values, object links) is untrusted and goes
// into the DOM through textContent only.
(function () {
  const $ = (s, el) => (el || document).querySelector(s);
  const $$ = (s, el) => Array.from((el || document).querySelectorAll(s));

  // h("div", {class: "x", text: "y", onclick: fn, "aria-label": "z"}, child, ...)
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === "class") el.className = v;
      else if (k === "text") el.textContent = v;
      else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? "" : String(v));
    }
    for (const c of kids.flat()) {
      if (c === null || c === undefined || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  const LOCAL = ["localhost", "127.0.0.1", "::1", "[::1]", ""];
  const HOST = LOCAL.includes(location.hostname) ? "lwm2m.fiumaralabs.com" : location.hostname;
  const PSK = "000102030405060708090a0b0c0d0e0f";
  // Module version for `go run ...@REF`; becomes "latest" once cmd/lwm2m-client is on main.
  const GO_REF = "deploy-demo";
  const announce = (t) => { $("#announce").textContent = t; };

  const S = {
    clients: [], max: 200, maxLt: 3600, skew: 0, online: null, first: true,
    sec: localStorage.getItem("lwm2m.sec") === "psk" ? "psk" : "nosec",
    client: localStorage.getItem("lwm2m.client") || "go",
    ep: localStorage.getItem("lwm2m.ep") || "",
    seen: new Set(),
  };
  const now = () => Date.now() + S.skew;

  // ---------- formatting ----------
  function ago(t) {
    const s = Math.max(0, Math.round((now() - new Date(t)) / 1000));
    if (s < 5) return "just now";
    if (s < 60) return s + " s ago";
    if (s < 3600) return Math.floor(s / 60) + " min ago";
    if (s < 86400) return Math.floor(s / 3600) + " h ago";
    return Math.floor(s / 86400) + " d ago";
  }
  function left(t) {
    const s = Math.round((new Date(t) - now()) / 1000);
    if (s <= 0) return "due";
    const m = Math.floor(s / 60), r = s % 60;
    if (m >= 60) return Math.floor(m / 60) + " h " + (m % 60) + " min";
    return m + ":" + String(r).padStart(2, "0");
  }
  function dur(sec) {
    if (sec % 3600 === 0 && sec >= 3600) return sec / 3600 + " h";
    if (sec % 60 === 0 && sec >= 60) return sec / 60 + " min";
    return sec + " s";
  }
  function clock(t) {
    const d = new Date(t);
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  }
  const UNITS = { Cel: "°C", Far: "°F", "%RH": "%RH", "%": "%", "/min": "/min", KB: "KiB" };
  const unit = (u) => (u ? UNITS[u] || u : "");
  function binding(c) {
    const b = c.binding || "U";
    const names = { U: "UDP", T: "TCP", S: "SMS", N: "Non-IP" };
    const parts = b.replace("Q", "").split("").map((x) => names[x] || x);
    return parts.join("+") + (c.queue ? " · queue" : "");
  }

  // ---------- API ----------
  async function api(path, opts) {
    const r = await fetch(path, Object.assign({ cache: "no-store", headers: { Accept: "application/json" } }, opts));
    let body = null;
    try { body = await r.json(); } catch (e) { body = null; }
    const rl = r.headers.get("X-RateLimit-Remaining");
    if (rl !== null) S.readsLeft = Number(rl);
    if (!r.ok) {
      const err = new Error((body && body.error) || "HTTP " + r.status);
      err.status = r.status;
      throw err;
    }
    return body;
  }
  const epURL = (ep) => "api/clients/" + encodeURIComponent(ep);

  // ---------- copy ----------
  async function copyText(text, btn) {
    try {
      await navigator.clipboard.writeText(text);
    } catch (e) {
      const ta = h("textarea", { class: "vh" });
      ta.value = text;
      document.body.append(ta);
      ta.select();
      try { document.execCommand("copy"); } catch (e2) { /* ignore */ }
      ta.remove();
    }
    if (btn) {
      const old = btn.dataset.label || btn.textContent;
      btn.dataset.label = old;
      btn.textContent = "Copied";
      btn.classList.add("done");
      clearTimeout(btn._t);
      btn._t = setTimeout(() => { btn.textContent = old; btn.classList.remove("done"); }, 1500);
    }
    announce("Copied to clipboard");
  }
  document.addEventListener("click", (ev) => {
    const b = ev.target.closest("[data-copy-from]");
    if (b) copyText($("#" + b.dataset.copyFrom).textContent, b);
  });

  // ---------- connect panel ----------
  const EP_OK = /^[A-Za-z0-9._:-]{1,64}$/;
  function newName() {
    const a = new Uint8Array(3);
    crypto.getRandomValues(a);
    return "demo-" + Array.from(a, (x) => x.toString(16).padStart(2, "0")).join("");
  }
  if (!EP_OK.test(S.ep)) S.ep = newName();

  const CLIENTS = {
    go: {
      intro: "A simulated battery-powered sensor from this repository: Device, Connectivity Monitoring, Location, Temperature, Humidity and Voltage, with values that change over time. No hardware needed; needs Go 1.21 or newer.",
      code: (ep, sec) => [`go run github.com/fiumaralabs/lwm2m/cmd/lwm2m-client@${GO_REF} \\`,
        sec === "psk" ? `  -server coaps://${HOST}:5684 -psk ${PSK} -ep ${ep}` : `  -server coap://${HOST}:5683 -ep ${ep}`],
      note: (sec) => [(sec === "psk" ? "-psk is the key in hex; the PSK identity is the endpoint name. " : "") +
        "It prints one line per server request, sends an Update every half lifetime (default 300 s) and deregisters on Ctrl-C. Run with -h for options (-lwm2m 1.0/1.1/1.2, -binding UQ, -send). ",
        link("Source", "https://github.com/fiumaralabs/lwm2m/tree/" + GO_REF + "/cmd/lwm2m-client")],
    },
    zephyr: {
      intro: "The lwm2m_client sample from the Zephyr tree. Replace <board> with your board target.",
      code: (ep, sec) => sec === "psk"
        ? ["west build -b <board> samples/net/lwm2m_client -- \\",
          "  -DEXTRA_CONF_FILE=overlay-dtls.conf \\",
          `  -DCONFIG_NET_SAMPLE_LWM2M_SERVER=\\"coaps://${HOST}:5684\\" \\`,
          `  -DCONFIG_NET_SAMPLE_LWM2M_ID=\\"${ep}\\"`, "west flash"]
        : ["west build -b <board> samples/net/lwm2m_client -- \\",
          `  -DCONFIG_NET_SAMPLE_LWM2M_SERVER=\\"coap://${HOST}:5683\\" \\`,
          `  -DCONFIG_NET_SAMPLE_LWM2M_ID=\\"${ep}\\"`, "west flash"],
      note: (sec) => sec === "psk"
        ? ["CONFIG_NET_SAMPLE_LWM2M_ID is both the endpoint name and the PSK identity, and CONFIG_NET_SAMPLE_LWM2M_PSK already defaults to the demo key. overlay-dtls.conf sets CONFIG_LWM2M_SECURITY_KEY_SIZE=32, so keep the name to 32 characters. ",
          link("Sample docs", "https://docs.zephyrproject.org/latest/samples/net/lwm2m_client/README.html")]
        : ["The sample uses IPv6 by default on QEMU; see the sample docs for IPv4. ",
          link("Sample docs", "https://docs.zephyrproject.org/latest/samples/net/lwm2m_client/README.html")],
    },
    ncs: {
      intro: "The nRF Connect SDK LwM2M client for nRF91 Series boards. Run it from the sdk-nrf tree. The endpoint name field does not apply: this sample names itself.",
      code: () => ["west build -b nrf9160dk/nrf9160/ns samples/cellular/lwm2m_client -- \\",
        `  -DCONFIG_LWM2M_CLIENT_UTILS_SERVER=\\"coaps://${HOST}:5684\\" \\`,
        "  -DCONFIG_LWM2M_ENGINE_DEFAULT_LIFETIME=3600", "west flash"],
      note: (sec) => [(sec === "nosec" ? "This sample is set up for DTLS, so the command uses DTLS-PSK. " : "") +
        "CONFIG_APP_LWM2M_PSK already defaults to the demo key. The endpoint name is urn:imei:<IMEI> (CONFIG_APP_ENDPOINT_PREFIX plus the modem IMEI) and it will be listed publicly on this page. The sample's default lifetime is 12 hours, above this server's cap, so the command lowers it. Use your own board target, e.g. nrf9151dk/nrf9151/ns. ",
        link("Sample docs", "https://docs.nordicsemi.com/bundle/ncs-latest/page/nrf/samples/cellular/lwm2m_client/README.html")],
    },
  };
  function link(text, href) { return h("a", { href, text }); }

  function kvRow(label, value, id) {
    return h("div", null, h("dt", { text: label }), h("dd", { id, text: value }),
      h("button", { type: "button", class: "copy", "data-copy-from": id, "aria-label": "Copy " + label.toLowerCase(), text: "Copy" }));
  }
  function renderConnect() {
    const ep = S.ep;
    const kv = $("#conn-kv");
    const rows = S.sec === "psk"
      ? [kvRow("Server URI", `coaps://${HOST}:5684`, "kv-uri"), kvRow("PSK identity", ep, "kv-id"), kvRow("PSK key (hex)", PSK, "kv-key")]
      : [kvRow("Server URI", `coap://${HOST}:5683`, "kv-uri"), kvRow("Endpoint name", ep, "kv-ep")];
    kv.replaceChildren(...rows);
    $$(".seg button").forEach((b) => b.setAttribute("aria-checked", String(b.dataset.sec === S.sec)));
    $$(".tabs button").forEach((b) => {
      const on = b.dataset.client === S.client;
      b.setAttribute("aria-selected", String(on));
      b.tabIndex = on ? 0 : -1;
    });
    const c = CLIENTS[S.client] || CLIENTS.go;
    $("#tp").setAttribute("aria-labelledby", "tab-" + S.client);
    $("#snip-intro").textContent = c.intro;
    $("#snip").textContent = c.code(ep, S.sec).join("\n");
    $("#snip-note").replaceChildren(...c.note(S.sec).map((x) => (typeof x === "string" ? document.createTextNode(x) : x)));
    $$("[data-maxlt]").forEach((e) => (e.textContent = dur(S.maxLt)));
  }

  function setupConnect() {
    const input = $("#ep-input");
    input.value = S.ep;
    input.addEventListener("input", () => {
      const v = input.value.trim();
      const ok = EP_OK.test(v);
      input.setAttribute("aria-invalid", String(!ok));
      $("#ep-help").textContent = ok
        ? "Used in every command on this page. Endpoint names are public here: do not use a real IMEI or serial number."
        : "Use 1 to 64 letters, digits, '.', '_', ':' or '-'.";
      if (!ok) return;
      S.ep = v;
      localStorage.setItem("lwm2m.ep", v);
      renderConnect();
    });
    $("#ep-new").addEventListener("click", () => {
      S.ep = newName();
      input.value = S.ep;
      input.removeAttribute("aria-invalid");
      localStorage.setItem("lwm2m.ep", S.ep);
      renderConnect();
    });
    $$(".seg button").forEach((b) => b.addEventListener("click", () => {
      S.sec = b.dataset.sec;
      localStorage.setItem("lwm2m.sec", S.sec);
      renderConnect();
    }));
    const tabs = $$(".tabs button");
    tabs.forEach((b, i) => {
      b.addEventListener("click", () => {
        S.client = b.dataset.client;
        localStorage.setItem("lwm2m.client", S.client);
        renderConnect();
      });
      b.addEventListener("keydown", (ev) => {
        const d = ev.key === "ArrowRight" ? 1 : ev.key === "ArrowLeft" ? -1 : 0;
        if (!d) return;
        ev.preventDefault();
        const n = tabs[(i + d + tabs.length) % tabs.length];
        n.focus();
        n.click();
      });
    });
    if (!CLIENTS[S.client]) S.client = "go";
    renderConnect();
  }

  // ---------- device list ----------
  function objectsLine(c) {
    const named = c.objects.filter((o) => o.id !== 0 && o.id !== 1 && o.id !== 21);
    const names = named.map((o) => (o.name || "Object " + o.id) + (o.instances > 1 ? " ×" + o.instances : ""));
    if (!names.length) return c.objects.length + " objects";
    const shown = names.slice(0, 4).join(", ");
    return names.length > 4 ? shown + " and " + (names.length - 4) + " more" : shown;
  }
  function renderList() {
    const box = $("#devices");
    const q = ($("#filter").value || "").trim().toLowerCase();
    $("#filter-wrap").hidden = S.clients.length < 6;
    $("#count").textContent = S.online ? S.clients.length + " of " + S.max : "";
    if (S.online === false) {
      box.replaceChildren(h("div", { class: "empty" }, h("h3", { text: "The server is not answering" }),
        h("p", { text: "The page retries every few seconds." })));
      return;
    }
    if (S.online === null) return;
    if (!S.clients.length) {
      box.replaceChildren(h("div", { class: "empty" },
        h("h3", { text: "No devices registered right now" }),
        h("p", { text: "Be the first. Without hardware, the quickest route is the demo client (one Go command):" }),
        h("ol", null,
          h("li", null, "Pick ", h("a", { href: "#connect", onclick: (e) => { e.preventDefault(); S.client = "go"; renderConnect(); $("#connect").scrollIntoView({ behavior: smooth() }); } , text: "Demo client (Go)" }), " above and copy the command."),
          h("li", { text: "Run it. The device appears here within a few seconds." }),
          h("li", { text: "Open it to browse its objects, read values and watch them live." }))));
      return;
    }
    const list = S.clients.filter((c) => !q || c.endpoint.toLowerCase().includes(q));
    const head = h("div", { class: "dhead", "aria-hidden": "true" },
      h("span", { text: "Endpoint" }), h("span", { text: "LwM2M" }), h("span", { text: "Transport" }),
      h("span", { text: "Security" }), h("span", { text: "Last seen" }), h("span", { text: "Expires in" }));
    const rows = list.map((c) => {
      const fresh = !S.first && !S.seen.has(c.endpoint);
      const exp = c.expires_at && c.expires_at !== "0001-01-01T00:00:00Z" ? c.expires_at : null;
      const soon = exp && new Date(exp) - now() < 60000;
      return h("a", { class: "drow" + (fresh ? " fresh" : "") + (c.endpoint === S.ep ? " mine" : ""), href: "#/d/" + encodeURIComponent(c.endpoint) },
        h("div", { class: "c-ep" }, h("div", { class: "ep", text: c.endpoint }), h("div", { class: "objs", text: objectsLine(c) })),
        h("div", null, h("span", { class: "cell-k", text: "LwM2M" }), h("span", { class: "tag", text: c.version })),
        h("div", null, h("span", { class: "cell-k", text: "Transport" }), binding(c)),
        h("div", null, h("span", { class: "cell-k", text: "Security" }), h("span", { class: "tag plain", text: c.security })),
        h("div", { class: "num" }, h("span", { class: "cell-k", text: "Last seen" }), h("span", { "data-ago": c.last_seen, text: ago(c.last_seen) })),
        h("div", { class: "num" + (soon ? " soon" : "") }, h("span", { class: "cell-k", text: "Expires in" }),
          exp ? h("span", { "data-left": exp, text: left(exp) }) : "never"));
    });
    if (!rows.length) rows.push(h("p", { class: "help", text: "No device matches the filter." }));
    box.replaceChildren(h("div", { class: "devices", role: "list" }, head, rows.map((r) => { r.setAttribute("role", "listitem"); return r; })));
    S.clients.forEach((c) => S.seen.add(c.endpoint));
    S.first = false;
  }

  function renderStatus() {
    const st = $("#status");
    st.classList.toggle("ok", S.online === true);
    st.classList.toggle("down", S.online === false);
    $("#status-text").textContent = S.online === null ? "Checking server…"
      : S.online ? `Online · ${S.clients.length} ${S.clients.length === 1 ? "device" : "devices"} registered · UDP 5683 (NoSec) and 5684 (DTLS-PSK)`
        : "Server not reachable, retrying";
  }

  async function refreshList() {
    try {
      const d = await api("api/clients");
      S.skew = new Date(d.now) - Date.now();
      S.max = d.max_registrations;
      S.maxLt = d.max_lifetime_s;
      S.clients = d.clients || [];
      S.online = true;
    } catch (e) {
      S.online = false;
    }
    renderStatus();
    if (!$("#view-home").hidden) renderList();
    $$("[data-maxlt]").forEach((e) => (e.textContent = dur(S.maxLt)));
  }

  // ---------- device view ----------
  const D = { ep: null, info: null, values: {}, raw: {}, errors: {}, live: {}, showRaw: false, gone: false, busy: {} };

  function resetDevice(ep) {
    for (const k of Object.keys(D.live)) stopLive(k);
    Object.assign(D, { ep, info: null, values: {}, raw: {}, errors: {}, live: {}, gone: false, busy: {}, autoRead: false });
  }

  // values are kept per node path: {v, type, value, at}
  function storeNodes(base, d, live) {
    for (const n of d.nodes || []) D.values[n.path] = { v: n.v, type: n.type, value: n.value, at: d.at, live };
    D.raw[base] = d;
  }

  async function readPath(path, btn) {
    if (D.busy[path]) return;
    D.busy[path] = true;
    if (btn) { btn.classList.add("busy"); btn.textContent = "Reading"; btn.disabled = true; }
    delete D.errors[path];
    try {
      const d = await api(epURL(D.ep) + "/read?path=" + encodeURIComponent(path));
      if (!d.code.startsWith("2")) D.errors[path] = "Client answered " + d.code + " " + coapName(d.code);
      else storeNodes(path, d, false);
      if (d.decode_error) D.errors[path] = "Decode error: " + d.decode_error;
      announce("Read " + path + ": " + d.code);
    } catch (e) {
      D.errors[path] = e.status === 429 ? "Rate limit reached. Reads refill over a minute." : e.message;
    }
    D.busy[path] = false;
    renderObjects();
    renderReadsLeft();
  }
  function coapName(code) {
    return ({ "2.05": "Content", "4.00": "Bad Request", "4.01": "Unauthorized", "4.04": "Not Found", "4.05": "Method Not Allowed",
      "4.06": "Not Acceptable", "5.00": "Internal Server Error", "5.01": "Not Implemented" })[code] || "";
  }

  // Live values: fetch() with a streamed body, so error replies keep their message.
  async function startLive(path) {
    if (D.live[path]) return;
    const ctl = new AbortController();
    D.live[path] = { ctl, state: "starting" };
    delete D.errors[path];
    renderObjects();
    try {
      const r = await fetch(epURL(D.ep) + "/observe?path=" + encodeURIComponent(path), { signal: ctl.signal, cache: "no-store" });
      const rl = r.headers.get("X-RateLimit-Remaining");
      if (rl !== null) { S.readsLeft = Number(rl); renderReadsLeft(); }
      if (!r.ok) {
        let msg = "HTTP " + r.status;
        try { msg = (await r.json()).error || msg; } catch (e) { /* keep */ }
        throw new Error(msg);
      }
      D.live[path].state = "on";
      renderObjects();
      announce("Live values on for " + path);
      const reader = r.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let i;
        while ((i = buf.indexOf("\n\n")) >= 0) {
          const block = buf.slice(0, i);
          buf = buf.slice(i + 2);
          let ev = "message", data = "";
          for (const line of block.split("\n")) {
            if (line.startsWith("event: ")) ev = line.slice(7);
            else if (line.startsWith("data: ")) data += line.slice(6);
          }
          if (!data) continue;
          const msg = JSON.parse(data);
          if (ev === "value") {
            if (msg.code && !msg.code.startsWith("2")) D.errors[path] = "Client answered " + msg.code;
            else storeNodes(path, msg, true);
            renderObjects(path);
          } else if (ev === "open") {
            D.live[path].until = msg.until;
          } else if (ev === "end") {
            D.errors[path] = "Live view ended: " + msg.reason + ".";
            ctl.abort();
          }
        }
      }
    } catch (e) {
      if (e.name !== "AbortError") D.errors[path] = "Live view failed: " + e.message;
    }
    delete D.live[path];
    if (D.ep) renderObjects();
  }
  function stopLive(path) {
    const l = D.live[path];
    if (l) { l.ctl.abort(); delete D.live[path]; }
  }

  function fmtValue(res, node, related) {
    if (!node) return null;
    const v = node.v;
    const t = node.type;
    const frag = h("span", null);
    if (t === "time") {
      const d = new Date(Number(v) * 1000);
      frag.append(isFinite(d) ? d.toISOString().replace("T", " ").replace(".000Z", " UTC") : String(v));
      frag.append(h("span", { class: "u", text: "(" + v + ")" }));
      return frag;
    }
    if (t === "opaque") {
      const hex = String(v || "");
      const bytes = hex.length / 2;
      frag.append(hex.length ? hex.match(/.{1,2}/g).slice(0, 48).join(" ") + (bytes > 48 ? " …" : "") : "(empty)");
      frag.append(h("span", { class: "u", text: bytes + " bytes" }));
      return frag;
    }
    if (t === "string" || t === "corelnk") {
      frag.append(v === "" ? "(empty string)" : String(v));
      return frag;
    }
    if (t === "float" && typeof v === "number") {
      const r6 = Math.round(v * 1e6) / 1e6;
      frag.append(Number.isInteger(r6) && Math.abs(r6) < 1e15 ? r6.toFixed(1) : String(r6));
    } else {
      frag.append(v === undefined ? node.value : String(v));
    }
    let u = res && res.units;
    if (!u && related) u = related;
    if (u) frag.append(h("span", { class: "u", text: unit(u) }));
    if (res && res.enum && res.enum[String(v)]) frag.append(h("span", { class: "enum", text: res.enum[String(v)] }));
    return frag;
  }

  function renderDevice() {
    const view = $("#view-device");
    const info = D.info;
    if (!info) {
      view.replaceChildren(h("div", { class: "w dv-top" }, h("a", { class: "back", href: "#/", text: "← All devices" }),
        h("p", { class: "help", text: D.gone ? "This device is not registered (it deregistered, expired, or the name is wrong)." : "Loading…" })));
      return;
    }
    const c = info.client;
    const exp = c.expires_at && c.expires_at !== "0001-01-01T00:00:00Z" ? c.expires_at : null;
    const meta = h("dl", { class: "meta" },
      h("div", null, h("dt", { text: "LwM2M" }), h("dd", { text: c.version })),
      h("div", null, h("dt", { text: "Transport" }), h("dd", { text: binding(c) })),
      h("div", null, h("dt", { text: "Security" }), h("dd", { text: c.security === "PSK" ? "DTLS-PSK" : "NoSec" })),
      h("div", null, h("dt", { text: "Lifetime" }), h("dd", null, dur(c.lifetime_s), exp ? h("small", null, "expires in ", h("span", { "data-left": exp, text: left(exp) })) : null)),
      h("div", null, h("dt", { text: "Registered" }), h("dd", null, h("span", { "data-ago": c.registered_at, text: ago(c.registered_at) }), h("small", { text: clock(c.registered_at) }))),
      h("div", null, h("dt", { text: "Last update" }), h("dd", null, h("span", { "data-ago": c.last_seen, text: ago(c.last_seen) }), h("small", { text: clock(c.last_seen) }))));

    const top = h("div", { class: "w dv-top" },
      h("a", { class: "back", href: "#/", text: "← All devices" }),
      h("div", { class: "dv-title" }, h("h1", { text: c.endpoint })),
      D.gone ? h("p", { class: "gone", role: "status", text: "This device is no longer registered. The values below are the last ones read." }) : null,
      meta, h("div", { id: "summary" }));

    const tools = h("div", { class: "w dv-tools" },
      h("h2", { text: "Objects" }),
      h("div", { class: "tools-r" },
        h("span", { id: "reads-left", class: "num" }),
        h("label", { class: "switch" }, h("input", { type: "checkbox", id: "rawt", checked: D.showRaw || null,
          onchange: (e) => { D.showRaw = e.target.checked; renderObjects(); } }), "Show raw responses")));

    const tree = h("nav", { class: "tree", "aria-label": "Objects" }, info.objects.map((o) =>
      h("a", { href: "#o-" + o.id, class: o.blocked ? "off" : "", onclick: (e) => { e.preventDefault(); const t = document.getElementById("o-" + o.id); if (t) t.scrollIntoView({ behavior: smooth(), block: "start" }); } },
        h("span", { text: o.model ? o.model.name : "Object " + o.id }), h("small", { text: "/" + o.id }))));

    const body = h("div", { class: "w dv-body" }, tree,
      h("div", { class: "objects-col" }, h("div", { class: "objects", id: "objects" }), h("div", { class: "objects extras", id: "extras" })));
    view.replaceChildren(h("div", { class: "view-in" }, top, tools, body));
    spy();
    renderObjects();
    renderSummary();
    renderReadsLeft();
  }

  // highlight the object in view in the tree
  let spyObs = null;
  function spy() {
    if (spyObs) spyObs.disconnect();
    if (!("IntersectionObserver" in window)) return;
    spyObs = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue;
        $$(".tree a").forEach((a) => a.classList.toggle("cur", a.getAttribute("href") === "#" + e.target.id));
      }
    }, { rootMargin: "-20% 0px -70% 0px" });
    $$("#objects section.obj").forEach((s) => spyObs.observe(s));
  }

  function renderReadsLeft() {
    const e = $("#reads-left");
    if (!e || !D.info) return;
    const lim = D.info.limits.reads_per_minute;
    e.textContent = S.readsLeft === undefined ? lim + " reads per minute" : S.readsLeft + " of " + lim + " reads left this minute";
  }

  function renderSummary() {
    const box = $("#summary");
    if (!box) return;
    const pick = [[0, "Manufacturer"], [1, "Model"], [3, "Firmware"], [17, "Device type"], [19, "Software"], [9, "Battery"], [16, "Bindings"]];
    const items = [];
    for (const [rid, label] of pick) {
      const n = D.values["/3/0/" + rid];
      if (!n) continue;
      items.push(h("div", null, h("dt", { text: label }), h("dd", { text: rid === 9 ? n.v + " %" : String(n.v) })));
    }
    box.replaceChildren(items.length ? h("dl", { class: "summary" }, items) : "");
  }

  function instancesOf(o) {
    const set = new Set(o.instances);
    const pre = "/" + o.id + "/";
    for (const p of Object.keys(D.values)) {
      if (p.startsWith(pre)) set.add(Number(p.split("/")[2]));
    }
    return Array.from(set).filter((x) => !isNaN(x)).sort((a, b) => a - b);
  }

  // resource IDs to show for an instance: the model's plus any seen in reads
  function resourcesOf(o, iid) {
    const byId = new Map();
    for (const r of (o.model ? o.model.resources : [])) byId.set(r.id, r);
    const pre = "/" + o.id + "/" + iid + "/";
    const seen = new Set();
    for (const p of Object.keys(D.values)) {
      if (p.startsWith(pre)) seen.add(Number(p.slice(pre.length).split("/")[0]));
    }
    const ids = new Set([...byId.keys(), ...seen]);
    const read = Object.keys(D.raw).some((k) => k === "/" + o.id || k === "/" + o.id + "/" + iid);
    return Array.from(ids).sort((a, b) => a - b)
      // after a read, hide optional resources the client did not return
      .filter((id) => {
        const r = byId.get(id);
        if (!read || seen.has(id) || !r) return true;
        if (r.ops === "-") return false; // Bootstrap-Server only
        return r.mandatory || !r.ops.includes("R");
      })
      .map((id) => byId.get(id) || { id, name: "Resource " + id, ops: "R", type: "", unknown: true });
  }

  function nodeFor(path) {
    if (D.values[path]) return { single: D.values[path] };
    const pre = path + "/";
    const multi = Object.keys(D.values).filter((p) => p.startsWith(pre))
      .sort((a, b) => Number(a.slice(pre.length)) - Number(b.slice(pre.length)))
      .map((p) => [p.slice(pre.length), D.values[p]]);
    return multi.length ? { multi } : null;
  }

  function actButtons(path, readable, observable) {
    const live = D.live[path];
    return h("span", { class: "act" },
      h("button", { type: "button", disabled: !readable || !!live || null, title: readable ? null : "Not readable", onclick: (e) => readPath(path, e.currentTarget), text: D.busy[path] ? "Reading" : "Read" }),
      observable ? h("button", { type: "button", class: live ? "on" : null, "aria-pressed": String(!!live),
        title: "Observe: the device sends new values as they change",
        onclick: () => { if (D.live[path]) { stopLive(path); renderObjects(); } else startLive(path); }, text: live ? (live.state === "starting" ? "Starting" : "Stop") : "Live" }) : null);
  }

  function resourceRow(o, iid, r) {
    const path = "/" + o.id + "/" + iid + "/" + r.id;
    const readable = r.ops.includes("R");
    const n = nodeFor(path);
    const err = D.errors[path];
    let val;
    // IPSO sensors: the unit of Sensor Value (5700) is in Sensor Units (5701)
    const rel = r.id === 5700 || r.id === 5601 || r.id === 5602 || r.id === 5603 || r.id === 5604
      ? (D.values["/" + o.id + "/" + iid + "/5701"] || {}).v : null;
    if (n && n.single) {
      val = h("div", { class: "val" }, fmtValue(r, n.single, rel), h("span", { class: "when" }, (n.single.live ? "notified " : "read ") , h("span", { "data-ago": n.single.at, text: ago(n.single.at) })));
    } else if (n && n.multi) {
      val = h("div", { class: "val" }, n.multi.map(([ri, nd]) => h("span", { class: "ri" }, h("b", { text: "[" + ri + "]" }), fmtValue(r, nd, rel))),
        h("span", { class: "when" }, "read ", h("span", { "data-ago": n.multi[0][1].at, text: ago(n.multi[0][1].at) })));
    } else if (!readable) {
      val = h("div", { class: "val none", text: r.ops === "E" ? "Execute only. Disabled on this demo." : r.ops === "W" ? "Write only. Disabled on this demo."
        : "Set by the Bootstrap-Server only" });
    } else {
      val = h("div", { class: "val none", text: "Not read yet" });
    }
    const live = D.live[path] && D.live[path].state === "on";
    return h("tr", { class: (readable ? "" : "dim") + (live ? " is-live" : ""), "data-path": path },
      h("td", { class: "c-id", text: r.id }),
      h("td", { class: "c-name" }, h("span", { class: "rname", text: r.name }),
        live ? h("span", { class: "livebadge" }, h("i", { "aria-hidden": "true" }), "Live") : null,
        h("span", { class: "rmeta", text: [r.ops === "-" ? "bootstrap" : r.ops, r.type === "none" ? "" : r.type, r.multiple ? "multiple" : "", r.mandatory ? "mandatory" : ""].filter(Boolean).join(" · "), title: r.description || null })),
      h("td", null, val, err ? h("div", { class: "val err", text: err }) : null),
      h("td", { class: "c-act" }, readable ? actButtons(path, true, !o.blocked) : null));
  }

  function renderObjects(flashPath) {
    const box = $("#objects");
    if (!box || !D.info) return;
    const scrollY = window.scrollY;
    const els = D.info.objects.map((o) => {
      const m = o.model;
      const opath = "/" + o.id;
      const head = h("div", { class: "obj-h" },
        h("div", null, h("h3", null, m ? m.name : "Object " + o.id, h("span", { class: "mono", text: opath + (m ? " · v" + m.version : "") })),
          m && m.description ? h("p", { class: "obj-desc", text: m.description }) : null),
        o.blocked ? null : h("span", { class: "act" }, h("button", { type: "button", onclick: (e) => readPath(opath, e.currentTarget), text: D.busy[opath] ? "Reading" : "Read object" })));
      const parts = [head];
      if (o.blocked) {
        parts.push(h("p", { class: "obj-note", text: (o.id === 0 ? "Security" : "OSCORE") + " object: it holds credentials, so this page never reads it." }));
        return h("section", { class: "obj", id: "o-" + o.id, "aria-label": (m ? m.name : "Object " + o.id) }, parts);
      }
      if (!m) parts.push(h("p", { class: "obj-note", text: "Not in the embedded OMA registry. Read it to see resource IDs and values." }));
      if (D.errors[opath]) parts.push(h("div", { class: "msg", text: D.errors[opath] }));
      const insts = instancesOf(o);
      if (!insts.length) parts.push(h("p", { class: "obj-note", text: "The registration lists no instances. Read the object to discover them." }));
      for (const iid of insts) {
        const ipath = opath + "/" + iid;
        parts.push(h("div", { class: "inst-h" }, h("h4", null, "Instance " + iid, h("span", { class: "mono", text: ipath })),
          h("span", { class: "act" }, h("button", { type: "button", onclick: (e) => readPath(ipath, e.currentTarget), text: D.busy[ipath] ? "Reading" : "Read instance" }))));
        if (D.errors[ipath]) parts.push(h("div", { class: "msg", text: D.errors[ipath] }));
        const rs = resourcesOf(o, iid);
        parts.push(h("table", { class: "res" },
          h("thead", null, h("tr", null, h("th", { class: "c-id", text: "ID" }), h("th", { class: "c-name", text: "Resource" }), h("th", { text: "Value" }), h("th", { class: "c-act" }, h("span", { class: "vh", text: "Actions" })))),
          h("tbody", null, rs.map((r) => resourceRow(o, iid, r)))));
      }
      if (D.showRaw) {
        const raws = Object.entries(D.raw).filter(([k]) => k === opath || k.startsWith(opath + "/"));
        parts.push(h("pre", { class: "raw", text: raws.length ? raws.map(([k, d]) => (D.live[k] ? "Observe " : "Read ") + k + "\n" + JSON.stringify(d, null, 2)).join("\n\n") : "No response yet. Read the object, an instance or a resource." }));
      }
      return h("section", { class: "obj", id: "o-" + o.id, "aria-label": (m ? m.name : "Object " + o.id) }, parts);
    });
    // keep focus on the button that was pressed across re-renders
    const fa = document.activeElement;
    const fkey = fa && fa.closest && fa.closest("[data-path]") ? fa.closest("[data-path]").dataset.path + "|" + fa.textContent : null;
    box.replaceChildren(...els);
    if (spyObs) { spyObs.disconnect(); els.forEach((s) => spyObs.observe(s)); }
    if (fkey) {
      const [p] = fkey.split("|");
      const row = box.querySelector(`[data-path="${CSS.escape(p)}"]`);
      const b = row && row.querySelector("button:not(:disabled)");
      if (b) b.focus({ preventScroll: true });
    }
    if (flashPath) {
      const row = box.querySelector(`[data-path="${CSS.escape(flashPath)}"]`);
      if (row) row.classList.add("flash");
    }
    window.scrollTo(0, scrollY);
    renderSummary();
    renderExtras();
  }

  function renderExtras() {
    const box = $("#extras");
    if (!box || !D.info) return;
    const parts = [];
    const s = D.info.last_send;
    if (s) {
      parts.push(h("section", { class: "obj" },
        h("div", { class: "obj-h" }, h("div", null, h("h3", null, "Last Send", h("span", { class: "mono", text: "/dp · " + s.content_format })),
          h("p", { class: "obj-desc" }, "Data the device pushed with the LwM2M 1.1 Send operation, ", h("span", { "data-ago": s.at, text: ago(s.at) }), "."))),
        h("table", { class: "res" }, h("tbody", null, s.nodes.map((n) => {
          const r = findRes(n.path);
          return h("tr", null, h("td", { class: "c-id", text: "" }), h("td", { class: "c-name" }, h("span", { class: "rname", text: r ? r.name : n.path }), h("span", { class: "rmeta mono", text: n.path })),
            h("td", null, h("div", { class: "val" }, fmtValue(r, n))), h("td", { class: "c-act" }));
        })))));
    }
    parts.push(h("details", { class: "obj rawlinks" }, h("summary", { class: "obj-h" }, h("h3", { text: "Registration payload" })),
      h("pre", { class: "raw", text: "Object links (CoRE link format):\n" + D.info.client.object_links +
        (D.info.client.content_formats ? "\n\nContent formats (ct): " + D.info.client.content_formats.join(", ") : "") +
        (D.info.client.root_path ? "\nAlternate path: " + D.info.client.root_path : "") })));
    box.replaceChildren(...parts);
  }
  function findRes(path) {
    const p = path.split("/").filter(Boolean).map(Number);
    const o = D.info.objects.find((x) => x.id === p[0]);
    return o && o.model ? o.model.resources.find((r) => r.id === p[2]) : null;
  }

  async function refreshDevice(first) {
    const ep = D.ep;
    try {
      const d = await api(epURL(ep));
      if (ep !== D.ep) return;
      S.skew = new Date(d.now) - Date.now();
      const objsChanged = !D.info || JSON.stringify(D.info.objects.map((o) => [o.id, o.instances])) !== JSON.stringify(d.objects.map((o) => [o.id, o.instances]));
      const wasGone = D.gone;
      D.info = d;
      D.gone = false;
      if (first || objsChanged || wasGone) renderDevice();
      else { renderMetaTimes(); renderExtras(); }
      if (first && !D.autoRead) {
        D.autoRead = true;
        const dev = d.objects.find((o) => o.id === 3);
        if (dev) readPath("/3/0");
      }
    } catch (e) {
      if (ep !== D.ep) return;
      if (e.status === 404) {
        D.gone = true;
        renderDevice();
      }
    }
  }
  function renderMetaTimes() {
    // last update / expiry changed: re-render only the meta block
    const c = D.info.client;
    const view = $("#view-device .meta");
    if (!view) return renderDevice();
    const exp = c.expires_at && c.expires_at !== "0001-01-01T00:00:00Z" ? c.expires_at : null;
    const spans = $$("[data-left]", view);
    if (exp && spans[0]) spans[0].dataset.left = exp;
    const agos = $$("[data-ago]", view);
    if (agos[1]) agos[1].dataset.ago = c.last_seen;
    const smalls = $$("dd small", view);
    if (smalls[2]) smalls[2].textContent = clock(c.last_seen);
  }

  // ---------- router ----------
  const smooth = () => (matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth");
  function route() {
    const m = location.hash.match(/^#\/d\/(.+)$/);
    $$(".site-nav a[data-nav]").forEach((a) => a.removeAttribute("aria-current"));
    if (m) {
      const ep = decodeURIComponent(m[1]);
      $("#view-home").hidden = true;
      $("#view-device").hidden = false;
      if (D.ep !== ep) { resetDevice(ep); renderDevice(); refreshDevice(true); }
      document.title = ep + " · LwM2M demo server · Fiumara Labs";
      window.scrollTo(0, 0);
      $("#main").focus({ preventScroll: true });
    } else {
      if (D.ep) resetDevice(null);
      $("#view-device").hidden = true;
      $("#view-device").replaceChildren();
      $("#view-home").hidden = false;
      document.title = "LwM2M demo server · Fiumara Labs";
      $('.site-nav a[data-nav="home"]').setAttribute("aria-current", "page");
      renderList();
      if (location.hash === "#connect") $("#connect").scrollIntoView();
    }
  }
  window.addEventListener("hashchange", route);
  $("#filter").addEventListener("input", renderList);

  // ---------- timers ----------
  function tick() {
    $$("[data-ago]").forEach((e) => { e.textContent = ago(e.dataset.ago); });
    $$("[data-left]").forEach((e) => { e.textContent = left(e.dataset.left); });
  }
  setInterval(tick, 1000);
  setInterval(() => {
    if (document.hidden) return;
    refreshList();
    if (D.ep) refreshDevice(false);
  }, 5000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) { refreshList(); if (D.ep) refreshDevice(false); } });

  setupConnect();
  route();
  refreshList();
})();
