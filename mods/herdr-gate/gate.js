// What both roles need to know about the gate: where its program is, whether
// it runs, what it is asking, and the calls that start, stop and answer it.

import { hc, exec, home, appHome, readTextFile, terminals, machines, fonts, notify, playSound, settings, shared, setShared, window as appWindow } from "herdr";
import * as fs from "fs/promises";

export const NAME = "herdr-gate";
export const ICON = `mods/${NAME}/gate.svg`;
export const DEFAULT_PORT = 7522;
const AUTOSTART = `${NAME}.autostart`;
const PORT = `${NAME}.port`;
const LANGUAGE = `${NAME}.language`;

// The language the gate program picks by itself on this computer, "en" or
// "zh": the system's, English when it is neither (see the gate's lang.go).
// Learned from `-info`. In module scope, so every copy of the mod shares it;
// English until the gate has answered.
let systemLanguage = "en";
// Whether that has been learned, from the gate program or without it.
let languageLearned = false;

/** What the person chose in the panel: "en", "zh", or "auto" for the
 *  system's language. Kept by Herdr. */
export function languageChoice() {
  const chosen = shared(LANGUAGE);
  return chosen === "en" || chosen === "zh" ? chosen : "auto";
}

/** Herdr draws every copy of the mod again, so all of it changes at once. */
export function setLanguageChoice(choice) {
  setShared(LANGUAGE, choice);
}

/** The language the panel and the button speak now, "en" or "zh". */
export function language() {
  const chosen = languageChoice();
  return chosen === "auto" ? systemLanguage : chosen;
}

/** What tells the gate program to speak the chosen language; nothing when
 *  it is to pick by itself. */
function languageArgs() {
  const chosen = languageChoice();
  return chosen === "auto" ? [] : ["-lang", chosen];
}

/** The language a running gate speaks: the one it was started with. Its log
 *  and its questions stay in it until it is started again. */
export function runningLanguage(terminal) {
  const argv = terminal?.argv ?? [];
  const at = argv.indexOf("-lang");
  const given = at >= 0 ? argv[at + 1] : null;
  return given === "en" || given === "zh" ? given : systemLanguage;
}

/** A message in the language shown. */
export function tr(en, zh) {
  return language() === "zh" ? zh : en;
}

/** A font of this system that draws Chinese well, or null. */
export function chineseFont() {
  const have = fonts();
  return ["Microsoft YaHei UI", "PingFang SC", "Noto Sans SC", "Noto Sans CJK SC", "Source Han Sans SC"].find((f) => have.includes(f)) ?? null;
}

/** The font for everything, when Chinese is what is shown; null to keep
 *  Herdr's (whose Latin font leaves Chinese to whatever fallback the system
 *  picks). */
export function textFont() {
  return language() === "zh" ? chineseFont() : null;
}

/** Whether the person asked for the gate to start whenever Herdr opens. Kept by Herdr. */
export function autostart() {
  return shared(AUTOSTART) === true;
}

export function setAutostart(on) {
  setShared(AUTOSTART, Boolean(on));
}

/** A port number from what someone typed, or null. */
export function parsePort(text) {
  const n = /^\d{1,5}$/.test(String(text).trim()) ? Number(text) : 0;
  return n >= 1 && n <= 65535 ? n : null;
}

/** The port the gate is started on. Kept by Herdr. */
export function port() {
  return parsePort(shared(PORT) ?? "") ?? DEFAULT_PORT;
}

export function setPort(n) {
  setShared(PORT, n);
}

/** The port a running gate listens on: what it was started with. */
export function runningPort(terminal) {
  const argv = terminal?.argv ?? [];
  const at = argv.indexOf("-listen");
  return (at >= 0 && parsePort(String(argv[at + 1] ?? "").replace(/^.*:/, ""))) || DEFAULT_PORT;
}

/** The gate's own folder: its fingerprint key, device list and connections. */
export function gateDir() {
  return `${home()}/.config/${NAME}`;
}

/**
 * How many devices are connected now, from the file a running gate rewrites
 * each time one connects or goes. A device with several connections counts
 * once. Only true while the gate runs: one cut short leaves its last list.
 */
export async function onlineDevices() {
  const text = await readTextFile(`${gateDir()}/connections.json`).catch(() => null);
  try {
    return new Set((JSON.parse(text || "{}").connections ?? []).map((c) => c.key)).size;
  } catch {
    return 0;
  }
}

/** Whether Herdr has heard from this computer's core yet. Before that, an
 *  empty terminal list says nothing about whether the gate runs. */
export function ready() {
  return machines().find((m) => m.name === null)?.layout === "ready";
}

/** The gate's terminal on this computer, if it runs. Live data: no polling. */
export function gateTerminal() {
  return terminals().find((t) => t.name === NAME && t.state !== "finished" && !String(t.address).includes("/")) ?? null;
}

/** The question the gate waits on, as it reported it to herdr (OSC 7501), or null. */
export function pending(terminal) {
  if (!terminal || terminal.status !== "blocked") return null;
  const report = terminal.report ?? {};
  return { title: report.title || tr("A new device wants to connect", "新设备请求连接"), message: report.msg || "" };
}

/** "windows", "macos" or "linux". There is no window during `herdr-app check`. */
function platform() {
  return appWindow()?.platform ?? (typeof process !== "undefined" ? process.platform : "linux");
}

// Whether the gate program has been made runnable since Herdr started.
let runnable = false;

// Where a copy of the mod without a gate program downloads one from: the
// newest release's program for this system.
const DOWNLOADS = "https://github.com/heromantf/herdr-gate/releases/latest/download/";

/** A path as this system writes it. */
function local(path) {
  return platform() === "windows" ? path.replace(/\//g, "\\") : path;
}

/**
 * The gate program for this computer: its file name, the mod's bin folder,
 * and its place in that folder. build.go puts it there, or a download does,
 * or the person does by hand.
 */
async function program() {
  const os = { windows: "windows", macos: "darwin", linux: "linux" }[platform()];
  let machine = typeof process !== "undefined" ? process.arch : "";
  if (!machine && os !== "windows") machine = (await exec("uname", ["-m"]).catch(() => null))?.stdout.trim() ?? "";
  const arch = /arm64|aarch64/.test(machine) ? "arm64" : "amd64";
  const file = `${NAME}-${os}-${arch}${os === "windows" ? ".exe" : ""}`;
  const bin = local(`${appHome()}/mods/${NAME}/bin`);
  return { os, file, bin, path: local(`${bin}/${file}`) };
}

/**
 * The gate program for this computer, ready to run. A copy from another
 * system arrives without its executable bit, and on macOS possibly
 * quarantined, so both are put right before its first use.
 */
async function gatePath() {
  const { os, path } = await program();
  if (os !== "windows" && !runnable) {
    await exec("chmod", ["+x", path]).catch(() => null);
    if (os === "darwin") await exec("xattr", ["-d", "com.apple.quarantine", path]).catch(() => null);
    runnable = true;
  }
  return path;
}

/** Whether this copy of the mod has no gate program for this computer: one
 *  taken from the repository comes without. */
export async function programMissing() {
  const { path } = await program();
  try {
    return !(await fs.exists(path));
  } catch {
    return false; // cannot tell: let running it say what is wrong
  }
}

/**
 * Learns the system's language without the gate program, for a copy of the
 * mod that has none to ask yet. It looks where the gate looks (its lang_*.go):
 * the display language on Windows, the first preferred language on macOS,
 * the locale elsewhere. Chinese is "zh", everything else "en".
 */
export async function learnSystemLanguage() {
  if (languageLearned) return;
  const ask = async (command, args, pick) => {
    const result = await exec(command, args, { timeout: 5000 }).catch(() => null);
    return result && result.code === 0 ? (pick(result.stdout) ?? "") : "";
  };
  let locale = "";
  if (platform() === "windows") {
    // The display language the person chose; without one, the system's own.
    // reg prints such a list with a backslash and a zero between its entries.
    const first = (out) => /REG_MULTI_SZ\s+(\S+)/.exec(out)?.[1]?.split("\\0")[0];
    locale =
      (await ask("reg", ["query", "HKCU\\Control Panel\\Desktop", "/v", "PreferredUILanguages"], first)) ||
      (await ask("reg", ["query", "HKCU\\Control Panel\\Desktop\\MuiCached", "/v", "MachinePreferredUILanguages"], first));
  } else if (platform() === "macos") {
    locale = await ask("defaults", ["read", "-g", "AppleLanguages"], (out) => /[A-Za-z]{2,3}(?:[-_][A-Za-z0-9]+)*/.exec(out)?.[0]);
  } else {
    locale = await ask("sh", ["-c", 'echo "${LC_ALL:-${LC_MESSAGES:-$LANG}}"'], (out) => out.trim());
  }
  if (!locale) return;
  systemLanguage = /^zh([-_.]|$)/i.test(locale) ? "zh" : "en";
  languageLearned = true;
}

/** The gate program as the panel tells of it: its file name, the folder it
 *  belongs in, and where it lies, or null when it is not there. */
export async function programInfo() {
  const { file, bin, path } = await program();
  return { file, bin, path: (await fs.exists(path).catch(() => false)) ? path : null };
}

/** A path for showing, with the home folder written as ~. */
export function shortPath(path) {
  const mine = local(home());
  return path.startsWith(mine) ? `~${path.slice(mine.length)}` : path;
}

/**
 * Downloads the gate program for this computer, the newest release's, into
 * the mod's bin folder. Only on a click, never by itself.
 */
export async function downloadGate() {
  const { os, file, bin, path } = await program();
  runnable = false; // a new file: its executable bit and quarantine mark are seen to again
  await fetchTo(DOWNLOADS + file, bin, path, os);
}

/**
 * Downloads a file and puts it at `path`, in the folder `dir`.
 *
 * Herdr starts a mod afresh a moment after anything in the mod's folder
 * changes, cutting short whatever the mod was doing. So the download waits
 * outside that folder until it is whole, and then one command of the
 * system's makes the folder if need be and moves the file in. The mod is
 * started afresh by that, and finds the program there. (Reading the file
 * into the script to write it out again would not do either: its bytes do
 * not come through whole.)
 */
export async function fetchTo(url, dir, path, os) {
  const part = local(`${appHome()}/${NAME}-download.part`);
  const meter = meterPath();
  try {
    // What an earlier download left behind says nothing about this one.
    await fs.unlink(meter).catch(() => null);
    let got;
    try {
      // curl comes with Windows, macOS and nearly every Linux; fetch() here returns text only.
      // Its progress meter, and whatever else it has to say, go to a file the panel reads meanwhile.
      // Ten minutes is the longest Herdr lets a program run.
      got = await exec("curl", ["--fail", "--location", "--show-error", "--stderr", meter, ...(await proxyArgs(os)), "--output", part, url], { timeout: 600000 });
    } catch (e) {
      throw new Error(tr(`curl could not be run, or took more than ten minutes: ${e?.message ?? e}`, `无法运行 curl，或者用时超过十分钟：${e?.message ?? e}`));
    }
    if (got.code !== 0) {
      const said = (await readTextFile(meter).catch(() => null)) ?? "";
      const why = said.split(/[\r\n]+/).filter((line) => line.startsWith("curl:")).pop() ?? `curl ${got.code}`;
      throw new Error(tr(`Download failed: ${why}`, `下载失败：${why}`));
    }
    const moved =
      os === "windows"
        ? await exec("cmd", ["/d", "/c", "mkdir", dir, "2>nul", "&", "move", "/y", part, path])
        : await exec("sh", ["-c", 'mkdir -p "$1" && mv -f "$2" "$3"', "sh", dir, part, path]);
    if (moved.code !== 0) {
      const why = (moved.stderr || moved.stdout || "").trim();
      throw new Error(tr(`The program could not be put in place (is herdr-gate still running?): ${why}`, `程序没能放到位（herdr-gate 还在运行吗？）：${why}`));
    }
  } finally {
    await fs.unlink(part).catch(() => null);
    await fs.unlink(meter).catch(() => null);
  }
}

/** Where curl writes how a download is going. Outside the mod's folder, like
 *  the download itself: a change inside it starts the mod afresh. */
function meterPath() {
  return local(`${appHome()}/${NAME}-download.progress`);
}

/** A number of bytes as curl's meter writes it: `51587`, `5477k`, `1330k`, `2.1M`. */
function meterBytes(text) {
  const m = /^(\d+(?:\.\d+)?)([kMGT]?)$/.exec(text);
  return m ? Number(m[1]) * 1024 ** ["", "k", "M", "G", "T"].indexOf(m[2]) : 0;
}

/** A number of bytes for showing: `812 KB`, `5.3 MB`. */
function showBytes(n) {
  return n >= 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : `${Math.round(n / 1024)} KB`;
}

/**
 * How far a running download is, from curl's own meter: the percentage, how
 * much has come and of how much, and the speed just now. Null while nothing
 * has come yet, and when no download runs.
 */
export async function downloadProgress() {
  const said = (await readTextFile(meterPath()).catch(() => null)) ?? "";
  // curl writes one line over and over; the file keeps every state of it.
  const last = said
    .split(/[\r\n]+/)
    .map((line) => line.trim().split(/\s+/))
    .filter((words) => words.length === 12 && /^\d+$/.test(words[0]))
    .pop();
  const total = last ? meterBytes(last[1]) : 0;
  if (!total) return null; // not past the redirects yet
  return { percent: Number(last[0]), received: showBytes(meterBytes(last[3])), total: showBytes(total), speed: `${showBytes(meterBytes(last[11]))}/s` };
}

/**
 * What makes curl go through the proxy this system is set to use, when
 * nothing in its environment says so already. curl takes a proxy from the
 * environment, not from the system's settings, and a Herdr opened from the
 * desktop has no such variables: the download then went out directly, on a
 * network that needs the proxy. macOS only so far.
 */
async function proxyArgs(os) {
  if (os !== "darwin") return [];
  const named = await exec("sh", ["-c", 'echo "$https_proxy$HTTPS_PROXY$all_proxy$ALL_PROXY"'], { timeout: 5000 }).catch(() => null);
  if (!named || named.code !== 0 || named.stdout.trim()) return [];
  const said = (await exec("scutil", ["--proxy"], { timeout: 5000 }).catch(() => null))?.stdout ?? "";
  const value = (key) => new RegExp(`^\\s*${key}\\s*:\\s*(\\S+)\\s*$`, "m").exec(said)?.[1];
  for (const [kind, scheme] of [["HTTPS", "http"], ["SOCKS", "socks5h"]]) {
    const host = value(`${kind}Proxy`), port = value(`${kind}Port`);
    if (value(`${kind}Enable`) === "1" && host && port) return ["--proxy", `${scheme}://${host.includes(":") ? `[${host}]` : host}:${port}`];
  }
  return [];
}

function failure(result) {
  try {
    return JSON.parse(result.stdout || result.stderr).error?.message ?? tr("hc did not succeed", "hc 没有成功");
  } catch {
    return (result.stderr || result.stdout || tr("hc did not succeed", "hc 没有成功")).trim();
  }
}

/** The gate program's own word on something (`-info`, `-revoke`). */
async function ask(args) {
  const result = await exec(await gatePath(), args, { timeout: 10000 });
  if (result.code !== 0) throw new Error((result.stderr || result.stdout || tr("herdr-gate did not succeed", "herdr-gate 没有成功")).trim());
  return result.stdout;
}

/**
 * This computer as other devices reach it: the account, its addresses
 * (Tailscale's first), its fingerprint, and the approved devices, each with
 * where it was approved from.
 */
export async function gateInfo() {
  // Asked without a language, so the answer names the one the gate picks by itself.
  const info = JSON.parse(await ask(["-info"]));
  systemLanguage = info.lang === "zh" ? "zh" : "en";
  languageLearned = true;
  info.addresses = info.addresses ?? [];
  // Those of the addresses that Tailscale itself says are its own.
  info.tailscale = info.tailscale ?? [];
  // Whether the person's own Tailscale devices are approved without the question.
  info.autoApprove = info.autoApprove === true;
  info.connections = info.connections ?? [];
  info.devices = (info.devices ?? []).map((d) => ({
    ...d,
    from: /from=(\S+)/.exec(d.note)?.[1]?.replace(/:\d+$/, "") ?? "",
    date: /\b(\d{4}-\d{2}-\d{2})\b/.exec(d.note)?.[1] ?? "",
    // Its name in Tailscale, for a device that was approved without the question.
    name: /tailscale=(\S+)/.exec(d.note)?.[1] ?? "",
  }));
  return info;
}

/**
 * Switches on or off that new devices signed in to Tailscale with this
 * computer's own account are approved without the question. The gate keeps
 * the switch itself (`-info` tells it), and a running one follows at once.
 */
export async function setAutoApprove(on) {
  await ask([...languageArgs(), "-auto-approve", on ? "on" : "off"]);
}

/** Takes a device off the list. A running gate drops its connection within seconds. */
export async function revokeDevice(device) {
  await ask([...languageArgs(), "-revoke", device.key]);
}

/**
 * Starts the gate in a terminal of herdr-core's with no pane in any tab:
 * closing a pane ends its terminal, and the gate must not end because someone
 * tidied a tab away. Without a pane Herdr's own attention rules pass it by, so
 * the launcher announces its questions instead (see `announce`).
 */
export async function startGate() {
  const path = await gatePath();
  // --no-parent: it belongs to nobody's terminal, so it ends with none.
  // No --keep: after a core restart a kept terminal comes back as a shell
  // under the same name, which would pass for a running gate.
  const result = await hc(["run", "--json", "--bg", "--no-parent", "--name", NAME, "--cwd", home(), "--", path, "-listen", `:${port()}`, ...languageArgs()]);
  if (result.code !== 0) throw new Error(failure(result));
}

export async function stopGate(terminal) {
  const result = await hc(["kill", "--json", terminal.address]);
  if (result.code !== 0) throw new Error(failure(result));
}

/**
 * Answers the gate's question in its terminal. --force: herdr-core refuses to
 * type into a program that reported "blocked", unless told it is the answer.
 */
export async function answer(terminal, allow) {
  const result = await hc(["send", "--json", "--force", terminal.address, allow ? "y" : "n"]);
  if (result.code !== 0) throw new Error(failure(result));
}

/**
 * Says that a device waits: a toast that opens the panel when clicked, and
 * the "needs you" sound when the person has sounds on.
 */
export function announce(waiting) {
  notify({ title: tr(`${NAME}: ${waiting.title}`, `${NAME}：${waiting.title}`), body: waiting.message, level: "warning", action: { name: "mod.open", args: { name: NAME } } });
  if (settings()?.sounds?.enabled !== false) playSound("blocked");
}

/**
 * The last lines of the gate's own window, oldest first. Read as text, never
 * shown as a terminal: nobody can type into it, stop it with Ctrl+C, or give
 * it a tab whose closing would end it.
 */
export async function gateLog(terminal, lines = 200) {
  const result = await hc(["read", terminal.address, "--lines", String(lines)]);
  if (result.code !== 0) throw new Error(failure(result));
  return result.stdout.split("\n").map((line) => line.trimEnd()).filter((line) => line.trim());
}
