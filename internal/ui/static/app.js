const escapeHTML = (value) => String(value ?? "").replace(
  /[&<>"']/g,
  (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char],
);
let state = { appliances: [], endpoints: [], jobs: [] };
let refreshing = false;
let liveEnabled = false;

async function api(path, method = "GET", body) {
  const response = await fetch(`/api/v1${path}`, {
    method,
    headers: { Accept: "application/json", ...(method !== "GET" ? { "Content-Type": "application/json" } : {}) },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || `${response.status} ${response.statusText}`);
  return result;
}

function notice(message, error = false) {
  const element = document.querySelector("#notice");
  element.textContent = message;
  element.className = error ? "notice error" : "notice";
}

function selectOptions(id, options) {
  const element = document.getElementById(id);
  const previous = element.value;
  const html = '<option value="">Select…</option>' + options.map(([value, label]) =>
    `<option value="${escapeHTML(value)}">${escapeHTML(label)}</option>`).join("");
  if (element.innerHTML !== html) {
    element.innerHTML = html;
    if (options.some(([value]) => value === previous)) element.value = previous;
  }
}

function label(id) {
  return state.appliances.find((a) => a.id === id)?.name || id;
}

function workflowOptions() {
  selectOptions("endpoint-appliance", state.appliances.map((a) => [a.id, a.name]));
  const endpoints = (role) => state.endpoints.filter((e) => e.role === role).map((e) => [e.appliance_id, label(e.appliance_id)]);
  selectOptions("endpoint-receiver", endpoints("receiver"));
  selectOptions("backup-source", endpoints("source"));
  selectOptions("restore-source", endpoints("source"));
  selectOptions("restore-target", endpoints("restore"));
  selectOptions("preflight-endpoint", state.endpoints.map((e) => [e.appliance_id, `${label(e.appliance_id)} (${e.role})`]));
  collectionOptions();
  approvals();
}

function collectionOptions() {
  const source = document.querySelector("#restore-source").value;
  selectOptions("restore-collection", state.jobs
    .filter((j) => j.state === "collected_unverified" && j.request.source_id === source)
    .map((j) => [j.id, `${j.request.timestamp} · ${j.id}`]));
}

function approvals() {
  const action = document.querySelector("#backup-action").value.toUpperCase();
  const source = document.querySelector("#backup-source").value;
  document.querySelector("#backup-approval").textContent = source ? `${action} ${source}` : "Select a source to see its approval phrase.";
  const target = document.querySelector("#restore-target").value;
  document.querySelector("#restore-approval").textContent = target ? `STAGE ${target}` : "Select a target to see its approval phrase.";
}

function table(headers, rows) {
  if (!rows.length) return '<div class="empty">No entries yet.</div>';
  return `<table><thead><tr>${headers.map((h) => `<th>${escapeHTML(h)}</th>`).join("")}</tr></thead><tbody>${
    rows.map((row) => `<tr>${row.map((cell) => `<td>${escapeHTML(cell)}</td>`).join("")}</tr>`).join("")
  }</tbody></table>`;
}

async function refresh() {
  if (refreshing) return;
  refreshing = true;
  const health = document.querySelector("#health");
  try {
    const [status, appliances, backups, capabilities] = await Promise.all([
      api("/health"), api("/appliances"), api("/backups"), api("/live/capabilities"),
    ]);
    const [endpoints, jobs] = capabilities.enabled
      ? await Promise.all([api("/live/endpoints"), api("/live/jobs")]) : [[], []];
    state = { appliances, endpoints, jobs };
    liveEnabled = capabilities.enabled;
    health.textContent = `${status.status} · ${status.version}`;
    health.classList.add("ok");
    document.querySelector("#live-warning").textContent = capabilities.warning;
    document.querySelectorAll("form button").forEach((b) => { b.disabled = !liveEnabled || b.closest("form").dataset.busy === "true"; });
    document.querySelector("#appliance-count").textContent = appliances.length;
    document.querySelector("#backup-count").textContent = jobs.filter((j) => j.state === "collected_unverified").length;
    document.querySelector("#qualified-count").textContent = backups.filter((b) => b.state === "restore_qualified").length;
    document.querySelector("#appliances").innerHTML = table(
      ["Name", "Host", "Volume", "SSH role", "Receiver"],
      appliances.map((a) => {
        const endpoint = endpoints.find((e) => e.appliance_id === a.id);
        return [a.name, a.hostname, a.volume_id, endpoint?.role || "Not configured", endpoint?.receiver_id ? label(endpoint.receiver_id) : "—"];
      }),
    );
    document.querySelector("#backups").innerHTML = table(
      ["Timestamp", "Source", "Provider", "State"],
      backups.map((b) => [b.native_timestamp, label(b.appliance_id), b.provider, b.state]),
    );
    document.querySelector("#jobs").innerHTML = table(
      ["Action / source", "State", "Phase", "Evidence / next steps"],
      jobs.map((j) => [`${j.request.action} · ${label(j.request.source_id)}`, j.state, j.phase, j.message || j.id]),
    );
    workflowOptions();
  } catch (error) {
    health.textContent = "Unavailable";
    health.classList.remove("ok");
    notice(error.message, true);
  } finally {
    refreshing = false;
  }
}

function bindForm(id, action) {
  const form = document.getElementById(id);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = form.querySelector("button[type=submit]");
    form.dataset.busy = "true";
    button.disabled = true;
    try {
      await action(form, Object.fromEntries(new FormData(form)));
      await refresh();
    } catch (error) {
      notice(error.message, true);
    } finally {
      form.dataset.busy = "false";
      button.disabled = !liveEnabled;
    }
  });
}

bindForm("register-form", async (form, data) => {
  const value = await api("/appliances", "POST", data);
  form.reset();
  notice(`Registered ${value.name}. Configure its SSH profile next.`);
});
bindForm("endpoint-form", async (_form, data) => {
  const appliance = state.appliances.find((a) => a.id === data.appliance_id);
  if (!appliance) throw new Error("Select a registered appliance.");
  const value = { ...data, host: appliance.hostname, port: Number(data.port) };
  if (value.role !== "source") delete value.receiver_id;
  await api(`/live/endpoints/${value.appliance_id}`, "PUT", value);
  notice("SSH profile saved. Run preflight before starting operations.");
});
bindForm("preflight-form", async (_form, data) => {
  const value = await api(`/live/endpoints/${data.endpoint_id}/preflight`, "POST", {});
  document.querySelector("#preflight-result").textContent = `${value.evidence}\n${value.warning}`;
  notice("Read-only preflight completed; review its limitations below.");
});
bindForm("settings-form", async (_form, data) => {
  await api("/live/settings", "PUT", {
    bandwidth_kib: Number(data.bandwidth_kib), timeout_minutes: Number(data.timeout_minutes),
  });
  notice("Transfer settings saved for future operations.");
});
bindForm("backup-form", async (form, data) => {
  const job = await api("/live/jobs", "POST", {
    action: data.action, source_id: data.source_id, confirmation: data.confirmation,
    quiesced: form.elements.quiesced.checked,
  });
  form.elements.confirmation.value = "";
  notice(`Operation accepted: ${job.id}. Follow its progress in operation history.`);
});
bindForm("restore-form", async (form, data) => {
  const job = await api("/live/jobs", "POST", {
    action: "stage", source_id: data.source_id, target_id: data.target_id,
    collection_id: data.collection_id, timestamp: data.timestamp, confirmation: data.confirmation,
    quiesced: form.elements.quiesced.checked, compatible_target: form.elements.compatible_target.checked,
  });
  form.elements.confirmation.value = "";
  notice(`Staging accepted: ${job.id}. Native restore is not executed automatically.`);
});

document.querySelectorAll("[data-tab]").forEach((button) => {
  button.addEventListener("click", () => {
    document.querySelectorAll("[data-panel]").forEach((panel) => { panel.hidden = panel.dataset.panel !== button.dataset.tab; });
    document.querySelectorAll("[data-tab]").forEach((tab) => tab.setAttribute("aria-pressed", String(tab === button)));
  });
});
document.querySelector("#refresh").addEventListener("click", refresh);
document.querySelector("#backup-source").addEventListener("change", approvals);
document.querySelector("#backup-action").addEventListener("change", approvals);
document.querySelector("#restore-target").addEventListener("change", approvals);
document.querySelector("#restore-source").addEventListener("change", collectionOptions);
document.querySelector("#restore-collection").addEventListener("change", () => {
  const selected = state.jobs.find((j) => j.id === document.querySelector("#restore-collection").value);
  document.querySelector("#restore-timestamp").value = selected?.request.timestamp || "";
});
document.querySelector("#endpoint-appliance").addEventListener("change", () => {
  const e = state.endpoints.find((value) => value.appliance_id === document.querySelector("#endpoint-appliance").value);
  if (!e) return;
  const form = document.querySelector("#endpoint-form");
  form.elements.role.value = e.role;
  form.elements.port.value = e.port;
  form.elements.key_ref.value = e.key_ref;
  form.elements.receiver_id.value = e.receiver_id || "";
});

async function initialize() {
  await refresh();
  try {
    const settings = await api("/live/settings");
    const form = document.querySelector("#settings-form");
    form.elements.bandwidth_kib.value = settings.bandwidth_kib;
    form.elements.timeout_minutes.value = settings.timeout_minutes;
  } catch (error) {
    notice(error.message, true);
  }
}
initialize();
setInterval(refresh, 5000);
