// Incremental updates, the Tarve way: executables built with `tarve build --packs` take app
// packs (`tarve pack`: only the app's code and files, a few hundred KB) listed in the signed
// latest.json that `tarve release` writes, and fall back to the whole executable when a
// release needs a newer runtime. The download happens in the background while playing; the
// update is installed when the app quits (or right away from the toast) and runs on the
// next launch, so music never stops for it.
import { canInstallUpdate, checkForUpdate, downloadUpdate, installUpdate, toast, type Update } from "@tarve/core";

/** The runtime this executable was built for: each has its own executable and app pack in a release. */
export const RUNTIME = process.versions.bun ? "bun" : "tjs" in globalThis ? "txiki" : "node";
/** The signed manifest for this runtime in the newest GitHub release (scripts/release.ts writes it); empty turns updates off. */
export const UPDATE_URL = `https://github.com/omarcosr/youtube-music/releases/latest/download/latest-${RUNTIME}.json`;
/** Public half of `youtube-music-update.key` (tarve keygen); releases signed with another key are rejected. */
export const UPDATE_PUBLIC_KEY = "Ax9hM9qQ6h1Sm3QKHTbdWBCyadL4zLGH26UqO/6ig8Y=";

const FIRST_CHECK = 15_000;
const EVERY = 6 * 60 * 60 * 1000;

let ready: { update: Update; file: string } | undefined;
let busy = false;

async function check(beforeRestart: () => void): Promise<void> {
  if (busy || ready) return;
  busy = true;
  try {
    const update = await checkForUpdate({ url: UPDATE_URL, publicKey: UPDATE_PUBLIC_KEY, signal: AbortSignal.timeout(60_000) });
    if (!update || !canInstallUpdate(update)) return;
    const file = await downloadUpdate(update);
    ready = { update, file };
    toast(`Update ${update.version} ready`, {
      description: "It installs when you quit.",
      duration: null,
      id: "update",
      action: { label: "Restart now", onClick: () => { beforeRestart(); install(true); } },
    });
  } catch (error) {
    // Offline, a release still uploading or a rejected signature: try again on the next round.
    console.warn("update check failed:", error instanceof Error ? error.message : error);
  } finally {
    busy = false;
  }
}

/** Checks soon after start and every few hours; `beforeRestart` saves what a restart would lose. */
export function watchUpdates(beforeRestart: () => void): () => void {
  if (!UPDATE_URL) return () => {};
  const first = setTimeout(() => void check(beforeRestart), FIRST_CHECK);
  const every = setInterval(() => void check(beforeRestart), EVERY);
  return () => { clearTimeout(first); clearInterval(every); };
}

/** Installs a downloaded update; `restart` starts it now, otherwise it runs on the next launch. */
export function install(restart = false): void {
  if (!ready) return;
  const { file } = ready;
  ready = undefined;
  try {
    installUpdate(file, { restart });
  } catch (error) {
    console.warn("update install failed:", error instanceof Error ? error.message : error);
  }
}
