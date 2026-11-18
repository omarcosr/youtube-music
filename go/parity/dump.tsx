// Prints the native documents the TypeScript view (src/) compiles to, for the Go parity test
// (go/ytm/parity_test.go): bun go/parity/dump.tsx <scenarios.json>
//
// Both sides must be the same compiler: the view is copied under this folder, whose tsconfig
// resolves @tarve/core to the Tarve checkout next to this repository (../tarve), the same
// sources the Go host is built against (go.mod's replace).
import { cpSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { jsx } from "../../../tarve/packages/core/src/jsx-runtime";
import { compileTree, invalidateMemos } from "../../../tarve/packages/core/src/reconciler";
import { withRenderScope } from "../../../tarve/packages/core/src/render-scope";

const copy = join(import.meta.dirname, ".cache", "youtube-music");
rmSync(copy, { recursive: true, force: true });
mkdirSync(copy, { recursive: true });
cpSync(join(import.meta.dirname, "../../src"), copy, { recursive: true });

const file = process.argv.slice(2).find(arg => arg.endsWith(".json"));
if (!file) throw new Error("usage: bun go/parity/dump.tsx <scenarios.json>");
const scenarios: { name: string; state: Record<string, unknown>; events?: { id: string; handler: string; args?: unknown[] }[] }[] = JSON.parse(readFileSync(file, "utf8"));
const view = await import("./.cache/youtube-music/youtube-music-view");
const fresh = JSON.parse(JSON.stringify(Object.fromEntries(Object.entries(view.state).filter(([, v]) => typeof v !== "function"))));
const out: unknown[] = [];
for (const scenario of scenarios) {
  for (const [key, value] of Object.entries(view.state)) if (typeof value !== "function") (view.state as Record<string, unknown>)[key] = undefined;
  Object.assign(view.state, JSON.parse(JSON.stringify(fresh)), JSON.parse(JSON.stringify(scenario.state)));
  const scope = {};
  const render = () => withRenderScope(scope, () => compileTree(jsx(view.YouTubeMusicView, {}), false, "auto", [], scope, true));
  let tree = render();
  for (const event of scenario.events ?? []) {
    const handler = (tree.handlers.get(event.id) as Record<string, (...args: unknown[]) => void> | undefined)?.[event.handler];
    if (!handler) throw new Error(`${scenario.name}: no ${event.handler} on ${event.id}`);
    invalidateMemos(scope, event.id);
    handler(...(event.args ?? []));
    tree = render();
  }
  out.push({ name: scenario.name, document: tree.document });
}
process.stdout.write(JSON.stringify(out));
