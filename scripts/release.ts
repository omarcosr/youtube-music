// Builds every Windows download of a release (installer and portable .exe) into dist/github, with the signed manifests the apps
// update from: the TypeScript app on txiki.js, Bun and Node.js (`tarve build --packs` + `tarve pack`:
// a new version reaches them as a small app pack) and the Go app (the whole executable).
//
//   bun scripts/release.ts             build into dist/github
//   bun scripts/release.ts --publish   also create GitHub release v<version> with gh
//
// Needs the update key (TARVE_UPDATE_PRIVATE_KEY or ./youtube-music-update.key), txiki.js (tjs or
// TARVE_TXIKI), Node.js 26.10+, Go, and for the Go app's native library either TARVE_GO_NATIVE or a
// Tarve checkout with Rust next to this repository (TARVE_DIR, default ../tarve, as go/go.mod).
import { createPrivateKey, createPublicKey, sign, verify } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const repo = "omarcosr/youtube-music";
const platform = "win32-x64";
const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
const version: string = pkg.version;
const name: string = pkg.tarve.name;
const tag = `v${version}`;
const base = `https://github.com/${repo}/releases/download/${tag}/`;
const publish = process.argv.includes("--publish");

const keyFile = join(root, "youtube-music-update.key");
const privateKey = process.env.TARVE_UPDATE_PRIVATE_KEY ?? (existsSync(keyFile) ? readFileSync(keyFile, "utf8") : undefined);
if (!privateKey) throw new Error("Needs TARVE_UPDATE_PRIVATE_KEY or ./youtube-music-update.key (bunx tarve keygen).");
const key = createPrivateKey({ key: Buffer.from(privateKey.trim(), "base64"), format: "der", type: "pkcs8" });
const spki = createPublicKey(key).export({ format: "der", type: "spki" });
const publicKey = Buffer.from(spki.subarray(spki.length - 32)).toString("base64");
// Both apps must carry this key, or they would reject the release.
for (const file of ["src/youtube-music-updates.ts", "go/update.go"]) {
  if (!readFileSync(join(root, file), "utf8").includes(publicKey)) throw new Error(`${file} does not hold the public key ${publicKey}`);
}

const env = { ...process.env, TARVE_UPDATE_PRIVATE_KEY: privateKey.trim() };
function run(command: string[], cwd = root): void {
  console.log(`$ ${command.join(" ")}`);
  const result = Bun.spawnSync(command, { cwd, env, stdio: ["inherit", "inherit", "inherit"] });
  if (result.exitCode !== 0) throw new Error(`${command[0]} failed (exit ${result.exitCode})`);
}

const build = join(root, "dist/build");
const out = join(root, "dist/github");
rmSync(build, { recursive: true, force: true });
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

interface Artifact { url: string }
// tarve release names files after the app ("YouTube Music-…"), and GitHub renames assets with spaces:
// copy them under plain names, point the manifest at the release's URLs and sign it again.
function release(flavor: string, exe: string, pack?: string): void {
  const updates = join(build, flavor, "updates");
  run(["bunx", "tarve", "release", "--version", version, "--name", name, "--public-key", publicKey,
    "--artifact", `${platform}=${exe}`, ...(pack ? ["--pack", pack] : []), "--out", updates]);
  const manifest = JSON.parse(JSON.parse(readFileSync(join(updates, "latest.json"), "utf8")).payload);
  const rename = (artifact: Artifact, asset: string) => {
    copyFileSync(join(updates, decodeURIComponent(artifact.url)), join(out, asset));
    artifact.url = base + asset;
  };
  for (const [os, artifact] of Object.entries<Artifact>(manifest.platforms)) {
    rename(artifact, `YouTubeMusic-${flavor}-${os}.exe`);
    // The Windows installer (tarve package --format setup): per user, Start menu, Apps & features, uninstaller.
    // Its wizard runs on txiki.js whatever the app's runtime (~20 MB instead of ~100 MB on Bun).
    const setup = join(build, flavor, "setup");
    run(["bunx", "tarve", "package", exe, "--format", "setup", "--runtime", "txiki", "--entry", "src/youtube-music.tsx", "--out", setup]);
    const [installer] = [...new Bun.Glob("*.exe").scanSync(setup)];
    if (!installer) throw new Error(`no installer in ${setup}`);
    copyFileSync(join(setup, installer), join(out, `YouTubeMusic-${flavor}-setup-${os}.exe`));
  }
  for (const [os, runtimes] of Object.entries<Record<string, Artifact>>(manifest.packs ?? {})) {
    for (const [runtime, artifact] of Object.entries(runtimes)) rename(artifact, `YouTubeMusic-${runtime}-${os}.tarve`);
  }
  const payload = JSON.stringify(manifest);
  const signature = sign(null, Buffer.from(payload), key);
  if (!verify(null, Buffer.from(payload), createPublicKey(key), signature)) throw new Error("manifest signature does not verify");
  writeFileSync(join(out, `latest-${flavor}.json`), JSON.stringify({ payload, signature: signature.toString("base64") }, null, 2) + "\n");
}

for (const runtime of ["txiki", "bun", "node"]) {
  const dir = join(build, runtime);
  const exe = join(dir, "YouTubeMusic.exe");
  const pack = join(dir, "YouTubeMusic.tarve");
  run(["bunx", "tarve", "build", "src/youtube-music.tsx", "--runtime", runtime, "--packs", "--outfile", exe]);
  run(["bunx", "tarve", "pack", "src/youtube-music.tsx", "--runtime", runtime, "--outfile", pack]);
  release(runtime, exe, pack);
}

// The Go app embeds the native library of the Tarve checkout it builds against (go/go.mod).
const goDir = join(root, "go");
const embedded = join(goDir, "release");
mkdirSync(embedded, { recursive: true });
let native = process.env.TARVE_GO_NATIVE;
if (!native) {
  native = join(build, "go", "tarve_native.dll");
  run(["bun", "scripts/build.ts", "--release", "--outfile", native], resolve(process.env.TARVE_DIR ?? join(root, "..", "tarve")));
}
copyFileSync(native, join(embedded, "tarve_native"));
copyFileSync(join(root, "assets/icon.png"), join(embedded, "icon.png"));
const goExe = join(build, "go", "YouTubeMusic.exe");
run(["go", "build", "-tags", "release", "-trimpath", "-ldflags", `-H windowsgui -s -w -X main.version=${version}`, "-o", goExe, "."], goDir);
release("go", goExe);

console.log(`\n${out}`);
if (publish) {
  const files = (ext: string) => [...new Bun.Glob(`*${ext}`).scanSync(out)].map(file => join(out, file));
  const exists = Bun.spawnSync(["gh", "release", "view", tag, "--repo", repo], { env, stdout: "ignore", stderr: "ignore" }).exitCode === 0;
  if (exists) {
    // Rebuilding a published release: replace its files, the manifests last.
    run(["gh", "release", "upload", tag, "--repo", repo, "--clobber", ...files(".exe"), ...files(".tarve")]);
    run(["gh", "release", "upload", tag, "--repo", repo, "--clobber", ...files(".json")]);
  } else {
    // A draft until every file is there: apps follow releases/latest, which skips drafts.
    run(["gh", "release", "create", tag, "--repo", repo, "--draft", "--title", `${name} ${version}`, "--generate-notes", ...files(".exe"), ...files(".tarve")]);
    run(["gh", "release", "upload", tag, "--repo", repo, ...files(".json")]);
    run(["gh", "release", "edit", tag, "--repo", repo, "--draft=false", "--latest"]);
  }
}
