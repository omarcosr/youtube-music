// Incremental updates, the Tarve way: executables built with `tarve build --packs` take app
// packs (`tarve pack`: only the app's code and files, a few hundred KB) listed in the signed
// manifest that scripts/release.ts publishes, and fall back to the whole executable when a
// release needs a newer runtime. The download happens in the background while playing; the
// update is installed when the app quits (or right away from About or the toast) and runs on
// the next launch, so music never stops for it. About (account menu) shows where it stands.
import { canInstallUpdate, checkForUpdate, downloadUpdate, installUpdate, toast, type Update } from "@tarve/core";
import { state, type UpdateStatus } from "./youtube-music-view";

/** The runtime this executable was built for: each has its own executable and app pack in a release. */
export const RUNTIME = process.versions.bun ? "bun" : "tjs" in globalThis ? "txiki" : "node";
const FLAVORS: Record<string, string> = { txiki: "txiki.js", bun: "Bun", node: "Node.js" };
/** The signed manifest for this runtime in the newest GitHub release (scripts/release.ts writes it); empty turns updates off. */
export const UPDATE_URL = `https://github.com/omarcosr/youtube-music/releases/latest/download/latest-${RUNTIME}.json`;
/** Public half of `youtube-music-update.key` (tarve keygen); releases signed with another key are rejected. */
export const UPDATE_PUBLIC_KEY = "Ax9hM9qQ6h1Sm3QKHTbdWBCyadL4zLGH26UqO/6ig8Y=";

const FIRST_CHECK = 15_000;
const EVERY = 6 * 60 * 60 * 1000;

/** The version `tarve build` (or `tarve run`, from package.json) baked in; "" when there is none. */
export function appVersion(): string {
  try {
    const metadata = JSON.parse(process.env.TARVE_APP_METADATA || "{}") as { version?: unknown };
    return typeof metadata.version === "string" ? metadata.version : "";
  } catch {
    return "";
  }
}

const message = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** The update cycle. */
export interface Updater {
  /** Checks now (`manual`: About's button); a check already running, or a download ready, keeps its state. */
  check(manual?: boolean): Promise<void>;
  /** Installs a downloaded update; `restart` starts it now, otherwise it runs on the next launch. */
  install(restart?: boolean): void;
}

/** What the cycle calls, injected so tests drive it without a network or a real executable. */
export interface UpdaterDeps {
  set(status: UpdateStatus): void;
  check(): Promise<Update | null>;
  canInstall(update: Update): boolean;
  download(update: Update, onProgress: (received: number, total: number) => void): Promise<string>;
  install(file: string, restart: boolean): void;
  /** A background check finished a download. */
  ready(update: Update): void;
}

export function createUpdater(deps: UpdaterDeps): Updater {
  let downloaded: { update: Update; file: string } | undefined;
  let busy = false;
  return {
    async check(manual = false) {
      if (busy) return;
      if (downloaded) { deps.set({ status: "ready", version: downloaded.update.version }); return; }
      busy = true;
      deps.set({ status: "checking" });
      try {
        const update = await deps.check();
        if (!update) { deps.set({ status: "current" }); return; }
        if (!deps.canInstall(update)) throw new Error("this copy can't replace itself (its folder is read-only)");
        const version = update.version;
        deps.set({ status: "downloading", version, progress: 0 });
        let shown = 0;
        const file = await deps.download(update, (received, total) => {
          const percent = total > 0 ? Math.floor((received / total) * 100) : -1;
          if (percent === shown) return;
          shown = percent;
          deps.set(percent < 0 ? { status: "downloading", version } : { status: "downloading", version, progress: percent / 100 });
        });
        downloaded = { update, file };
        deps.set({ status: "ready", version });
        if (!manual) deps.ready(update);
      } catch (error) {
        // Offline, a release still uploading or a rejected signature: About says so, and the next round tries again.
        deps.set({ status: "error", error: message(error) });
        console.warn("update failed:", message(error));
      } finally {
        busy = false;
      }
    },
    install(restart = false) {
      if (!downloaded) return;
      const { file } = downloaded;
      downloaded = undefined;
      try {
        deps.install(file, restart);
      } catch (error) {
        deps.set({ status: "error", error: message(error) });
        console.warn("update install failed:", message(error));
      }
    },
  };
}

let updater: Updater | undefined;

/** Checks soon after start and every few hours; `beforeRestart` saves what a restart would lose. */
export function watchUpdates(beforeRestart: () => void): () => void {
  state.appVersion = appVersion();
  state.flavor = FLAVORS[RUNTIME] ?? RUNTIME;
  const set = (status: UpdateStatus) => { state.update = status; state.refresh(); };
  if (!UPDATE_URL || !state.appVersion || !canInstallUpdate()) { set({ status: "off" }); return () => { }; }
  const restart = () => { beforeRestart(); updater?.install(true); };
  updater = createUpdater({
    set,
    check: () => checkForUpdate({ url: UPDATE_URL, publicKey: UPDATE_PUBLIC_KEY, signal: AbortSignal.timeout(60_000) }),
    canInstall: update => canInstallUpdate(update),
    download: (update, onProgress) => downloadUpdate(update, { onProgress }),
    install: (file, now) => installUpdate(file, { restart: now }),
    // About shows it when open; otherwise a toast says so, once.
    ready: update => {
      if (state.about) return;
      toast(`Update ${update.version} ready`, {
        description: "It installs when you quit.", duration: null, id: "update",
        action: { label: "Restart now", onClick: restart },
      });
    },
  });
  state.checkUpdates = () => void updater?.check(true);
  state.restartToUpdate = restart;
  set({ status: "idle" });
  const first = setTimeout(() => void updater?.check(), FIRST_CHECK);
  const every = setInterval(() => void updater?.check(), EVERY);
  return () => { clearTimeout(first); clearInterval(every); };
}

/** Installs a downloaded update when the app quits; it runs on the next launch. */
export function install(restart = false): void {
  updater?.install(restart);
}
