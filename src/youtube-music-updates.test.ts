import { afterAll, describe, expect, test } from "bun:test";
import { checkForUpdate, downloadUpdate, type Update } from "@tarve/core";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createUpdater, type UpdaterDeps } from "./youtube-music-updates";
import type { UpdateStatus } from "./youtube-music-view";

const update = (version = "0.2.0"): Update => ({
  kind: "pack", url: "https://example.com/app.tarve", sha256: "", size: 10, version, currentVersion: "0.1.0", pubDate: "2026-01-01T00:00:00Z", platform: "win32-x64",
});

function harness(overrides: Partial<UpdaterDeps> = {}) {
  const statuses: UpdateStatus[] = [];
  const installed: [string, boolean][] = [];
  const announced: string[] = [];
  const updater = createUpdater({
    set: status => statuses.push(status),
    check: async () => update(),
    canInstall: () => true,
    download: async (_, onProgress) => { onProgress(5, 10); onProgress(10, 10); return "C:/app.new"; },
    install: (file, restart) => installed.push([file, restart]),
    ready: next => announced.push(next.version),
    ...overrides,
  });
  return { updater, statuses, installed, announced };
}

describe("updater", () => {
  test("checks, downloads with progress, and is ready", async () => {
    const { updater, statuses, announced } = harness();
    await updater.check();
    expect(statuses.map(s => s.status)).toEqual(["checking", "downloading", "downloading", "downloading", "ready"]);
    expect(statuses.filter(s => s.status === "downloading").map(s => s.progress)).toEqual([0, 0.5, 1]);
    expect(statuses.at(-1)).toEqual({ status: "ready", version: "0.2.0" });
    expect(announced).toEqual(["0.2.0"]);
  });

  test("a manual check is not announced with a toast (About shows it)", async () => {
    const { updater, announced } = harness();
    await updater.check(true);
    expect(announced).toEqual([]);
  });

  test("up to date", async () => {
    const { updater, statuses } = harness({ check: async () => null });
    await updater.check(true);
    expect(statuses.map(s => s.status)).toEqual(["checking", "current"]);
  });

  test("an error is shown, and the next check tries again", async () => {
    let fail = true;
    const { updater, statuses } = harness({ check: async () => { if (fail) throw new Error("offline"); return null; } });
    await updater.check();
    expect(statuses.at(-1)).toEqual({ status: "error", error: "offline" });
    fail = false;
    await updater.check(true);
    expect(statuses.at(-1)).toEqual({ status: "current" });
  });

  test("a copy that can't replace itself says why", async () => {
    const { updater, statuses } = harness({ canInstall: () => false });
    await updater.check();
    expect(statuses.at(-1)?.status).toBe("error");
    expect(statuses.at(-1)?.error).toContain("read-only");
  });

  test("a check while one runs does nothing; a ready download is not fetched again", async () => {
    let checks = 0;
    let release!: () => void;
    const { updater, statuses } = harness({ check: async () => { checks++; await new Promise<void>(r => { release = r; }); return update(); } });
    const first = updater.check();
    await updater.check(true);
    release();
    await first;
    await updater.check(true);
    expect(checks).toBe(1);
    expect(statuses.at(-1)).toEqual({ status: "ready", version: "0.2.0" });
  });

  test("installs once: now with a restart, or on quit", async () => {
    const { updater, installed } = harness();
    updater.install(true);
    expect(installed).toEqual([]);
    await updater.check();
    updater.install(true);
    updater.install();
    expect(installed).toEqual([["C:/app.new", true]]);
  });
});

// The real @tarve/core client against a manifest laid out as scripts/release.ts publishes it.
describe("release manifest", () => {
  const dir = mkdtempSync(join(tmpdir(), "ytm-updates-"));
  const exe = new Uint8Array([0x4d, 0x5a, ...new TextEncoder().encode("a new YouTube Music")]);
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const spki = publicKey.export({ format: "der", type: "spki" });
  const publicBase64 = Buffer.from(spki.subarray(spki.length - 32)).toString("base64");
  let manifest = "";
  const server = Bun.serve({
    port: 0, hostname: "127.0.0.1",
    fetch: request => new URL(request.url).pathname === "/latest-go.json" ? new Response(manifest) : new Response(exe),
  });
  const base = `http://localhost:${server.port}/`;
  const publish = (version: string, name = "YouTube Music") => {
    const payload = JSON.stringify({
      name, version, pubDate: new Date().toISOString(),
      platforms: { "win32-x64": { url: `${base}YouTubeMusic-go-win32-x64.exe`, sha256: createHash("sha256").update(exe).digest("hex"), size: exe.length } },
    });
    manifest = JSON.stringify({ payload, signature: sign(null, Buffer.from(payload), privateKey).toString("base64") });
  };
  const options = { url: `${base}latest-go.json`, publicKey: publicBase64, currentVersion: "0.1.4", platform: "win32-x64", name: "YouTube Music" };
  afterAll(() => { server.stop(true); rmSync(dir, { recursive: true, force: true }); });

  test("a newer release is offered and downloads, checked against its hash", async () => {
    publish("0.1.5");
    const found = await checkForUpdate(options);
    expect(found?.version).toBe("0.1.5");
    expect(found?.kind).toBe("executable");
    const file = await downloadUpdate(found!, { path: join(dir, "YouTubeMusic.exe.new") });
    expect(new Uint8Array(readFileSync(file))).toEqual(exe);
  });

  test("the same version is not offered", async () => {
    publish("0.1.4");
    expect(await checkForUpdate(options)).toBeNull();
  });

  test("a manifest signed with another key or for another app is refused", async () => {
    publish("0.1.5");
    const other = generateKeyPairSync("ed25519").publicKey.export({ format: "der", type: "spki" });
    await expect(checkForUpdate({ ...options, publicKey: Buffer.from(other.subarray(other.length - 32)).toString("base64") })).rejects.toThrow();
    publish("0.1.5", "Another App");
    await expect(checkForUpdate(options)).rejects.toThrow();
  });
});
