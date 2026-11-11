// What both roles need to know about the gate: where its program is, whether
// it runs, what it is asking, and the calls that start, stop and answer it.

import { hc, exec, home, appHome, readTextFile, terminals, machines, fonts, notify, playSound, settings, shared, setShared, window as appWindow } from "herdr";

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

/**
 * The gate program for this computer, beside this mod, ready to run. A copy
 * from another system arrives without its executable bit, and on macOS
 * possibly quarantined, so both are put right before its first use.
 */
async function gatePath() {
  const os = { windows: "windows", macos: "darwin", linux: "linux" }[platform()];
  let machine = typeof process !== "undefined" ? process.arch : "";
  if (!machine && os !== "windows") machine = (await exec("uname", ["-m"]).catch(() => null))?.stdout.trim() ?? "";
  const arch = /arm64|aarch64/.test(machine) ? "arm64" : "amd64";
  const path = `${appHome()}/mods/${NAME}/bin/${NAME}-${os}-${arch}`;
  if (os === "windows") return `${path}.exe`.replace(/\//g, "\\");
  if (!runnable) {
    await exec("chmod", ["+x", path]).catch(() => null);
    if (os === "darwin") await exec("xattr", ["-d", "com.apple.quarantine", path]).catch(() => null);
    runnable = true;
  }
  return path;
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
