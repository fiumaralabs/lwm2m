"use strict";
// Uses textContent everywhere: endpoint names and object links are untrusted.
(function () {
  const host = location.hostname;
  if (host && host !== "localhost" && host !== "127.0.0.1") {
    document.querySelectorAll(".host").forEach((e) => (e.textContent = host));
    document.querySelectorAll("pre").forEach((e) => (e.textContent = e.textContent.split("lwm2m.fiumaralabs.com").join(host)));
  }
  const tbody = document.querySelector("#clients tbody");
  let current = "";

  function ago(t, now) {
    const s = Math.max(0, Math.round((now - new Date(t)) / 1000));
    if (s < 60) return s + " s ago";
    if (s < 3600) return Math.round(s / 60) + " min ago";
    return Math.round(s / 3600) + " h ago";
  }
  function cell(tr, text, cls) {
    const td = document.createElement("td");
    td.textContent = text;
    if (cls) td.className = cls;
    tr.appendChild(td);
  }
  async function refresh() {
    try {
      const r = await fetch("api/clients", { cache: "no-store" });
      const d = await r.json();
      const now = new Date(d.now);
      document.getElementById("max").textContent = d.max_registrations;
      document.getElementById("maxlt").textContent = d.max_lifetime_s;
      document.getElementById("count").textContent = d.clients.length + " / " + d.max_registrations;
      tbody.replaceChildren();
      for (const c of d.clients) {
        const tr = document.createElement("tr");
        cell(tr, c.endpoint, "ep");
        cell(tr, c.version);
        cell(tr, c.binding + (c.queue ? " (Q)" : ""));
        cell(tr, c.security);
        cell(tr, c.lifetime_s + " s");
        cell(tr, ago(c.registered_at, now));
        cell(tr, ago(c.last_seen, now));
        cell(tr, c.object_links, "links");
        tr.addEventListener("click", () => select(c.endpoint));
        tbody.appendChild(tr);
      }
      document.getElementById("empty").hidden = d.clients.length > 0;
    } catch (e) {
      document.getElementById("count").textContent = "offline";
    }
  }
  function select(ep) {
    current = ep;
    document.getElementById("rep").textContent = ep;
    document.getElementById("rout").textContent = "";
    document.getElementById("reader").hidden = false;
    document.getElementById("reader").scrollIntoView({ behavior: "smooth" });
  }
  document.getElementById("readform").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const out = document.getElementById("rout");
    out.textContent = "reading…";
    const path = document.getElementById("rpath").value.trim();
    try {
      const r = await fetch("api/clients/" + encodeURIComponent(current) + "/read?path=" + encodeURIComponent(path));
      const d = await r.json();
      if (d.error) { out.textContent = "error: " + d.error; return; }
      let s = "CoAP " + d.code + (d.content_format !== undefined ? "  (content-format " + d.content_format + ")" : "") + "\n";
      for (const n of d.nodes || []) s += n.path + " = " + n.value + (n.type ? "  [" + n.type + "]" : "") + "\n";
      if (d.decode_error) s += "decode error: " + d.decode_error + "\n";
      out.textContent = s;
    } catch (e) {
      out.textContent = "error: " + e;
    }
  });
  refresh();
  setInterval(refresh, 5000);
})();
