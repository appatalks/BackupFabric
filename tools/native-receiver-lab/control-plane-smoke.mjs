import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const base = process.env.BACKUPFABRIC_API_URL || "http://127.0.0.1:8088";
const timeoutMs = Number(process.env.BACKUPFABRIC_SMOKE_TIMEOUT_MS || 1800000);
assert.ok(Number.isSafeInteger(timeoutMs) && timeoutMs >= 1000 && timeoutMs <= 86400000, "Smoke timeout must be an integer between 1000 and 86400000 milliseconds");
assert.ok(process.env.BACKUPFABRIC_SMOKE_SOURCES, "Set BACKUPFABRIC_SMOKE_SOURCES to a deployment-specific source fixture JSON file");
const sources = JSON.parse(await readFile(process.env.BACKUPFABRIC_SMOKE_SOURCES, "utf8"));
assert.ok(Array.isArray(sources) && sources.length > 0, "Supply at least one source fixture");
for (const source of sources) {
  for (const field of ["name", "hostname", "volume_id", "appliance_uuid", "key", "version", "route"]) assert.ok(typeof source[field] === "string" && source[field], `Missing fixture field: ${field}`);
}

async function request(path, method = "GET", body, expected) {
  const response = await fetch(`${base}/api/v1${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(15000),
  });
  const value = await response.json();
  if (expected) assert.equal(response.status, expected, JSON.stringify(value));
  else assert.ok(response.ok, JSON.stringify(value));
  return value;
}

async function waitForJob(id) {
  let phase = "";
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const job = (await request("/live/jobs")).find((value) => value.id === id);
    assert.ok(job, "accepted job must remain in the journal");
    if (job.phase !== phase) {
      console.log(`${id}: ${job.phase}`);
      phase = job.phase;
    }
    if (job.state !== "running") return job;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error(`operation did not finish within smoke-test deadline: ${id}`);
}

assert.equal((await request("/health")).status, "ok");
assert.equal((await request("/live/capabilities")).native_push, true);
const registered = await request("/appliances");
for (const source of sources) {
  let appliance = registered.find((value) => value.hostname === source.hostname);
  if (!appliance) {
    const { name, hostname, volume_id, appliance_uuid } = source;
    appliance = await request("/appliances", "POST", { name, hostname, volume_id, appliance_uuid });
  }
  source.id = appliance.id;
  await request(`/live/endpoints/${source.id}`, "PUT", {
    appliance_id: source.id, role: "source", host: source.hostname, port: 122,
    key_ref: source.key, collection_mode: "native_push",
  });
  const preflight = await request(`/live/endpoints/${source.id}/preflight`, "POST", {});
  assert.equal(preflight.endpoint_id, source.id);
  const profile = (await request("/live/endpoints")).find((value) => value.appliance_id === source.id);
  assert.equal(profile.native_source_uuid, source.appliance_uuid);
  assert.equal(profile.native_route, source.route);
  const published = (await request("/live/snapshots")).find((value) => value.source_id === source.id && value.current);
  assert.ok(published, "each native route must have a current snapshot");
  assert.equal(published.version, source.version);
  assert.equal(published.uuid, source.appliance_uuid);
  assert.equal(published.state, "received_unverified");
  console.log(`${source.name}: registered, pinned SSH preflight passed, ${published.timestamp} ${published.version}`);
  await request("/live/jobs", "POST", { action: "verify", source_id: source.id, confirmation: `VERIFY ${source.id}`, quiesced: false }, 400);
  if (process.argv.includes("--backup")) {
    const accepted = await request("/live/jobs", "POST", { action: "backup", source_id: source.id, confirmation: `BACKUP ${source.id}` });
    const job = await waitForJob(accepted.id);
    assert.equal(job.state, "received_unverified", JSON.stringify(job));
    assert.ok(job.native_snapshot.timestamp > published.timestamp, "fresh backup must advance the receiver timestamp");
    assert.equal(job.native_snapshot.uuid, source.appliance_uuid);
    assert.equal(job.native_snapshot.version, source.version);
    const current = (await request("/live/snapshots")).find((value) => value.source_id === source.id && value.current);
    assert.equal(current.timestamp, job.native_snapshot.timestamp);
    console.log(`${source.name}: fresh backup created, delivered, and journaled (${current.timestamp}, ${job.id})`);
  }
  if (process.argv.includes("--archive")) {
    const accepted = await request("/live/jobs", "POST", { action: "archive", source_id: source.id, confirmation: `ARCHIVE ${source.id}` });
    const job = await waitForJob(accepted.id);
    assert.equal(job.state, "received_unverified", JSON.stringify(job));
    assert.equal(job.native_snapshot.uuid, source.appliance_uuid);
    assert.equal(job.native_snapshot.version, source.version);
    console.log(`${source.name}: control-plane native archive completed and journaled (${job.id})`);
  }
  if (process.argv.includes("--verify-and-collect")) {
    for (const action of ["verify", "collect"]) {
      const accepted = await request("/live/jobs", "POST", {
        action, source_id: source.id, confirmation: `${action.toUpperCase()} ${source.id}`, quiesced: true,
      });
      const job = await waitForJob(accepted.id);
      assert.equal(job.state, action === "verify" ? "checksum_verified_unqualified" : "collected_unverified", JSON.stringify(job));
      assert.equal(job.native_snapshot.uuid, source.appliance_uuid);
      assert.equal(job.native_snapshot.version, source.version);
      console.log(`${source.name}: ${action} completed with state ${job.state} (${job.id})`);
    }
  }
}
assert.equal((await request("/backups")).filter((value) => value.state === "restore_qualified").length, 0);
console.log(`PASS: ${sources.length}-source inventory, persisted bindings, source preflights, and unapproved verification rejection`);