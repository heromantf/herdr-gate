// The herdr-gate button for a layout's status bar (props { role: "launcher" }):
// the gate's state in one icon, and a click that opens the panel. While
// devices are connected it says how many; while one waits for approval it
// says that instead, blinking in the theme's "needs you" color. With props
// { block: "<its block id>" } it sizes its own block (layout.resize) to what
// it shows: the icon alone, the icon and a count, or the icon and a sentence.

import { div, svg } from "gpui-kit";
import { h_flex } from "gpui-base";
import { props, theme, run, notify, watchFolder } from "herdr";
import { NAME, ICON, tr, textFont, gateTerminal, pending, announce, autostart, ready, startGate, gateDir, onlineDevices, gateInfo, programMissing } from "./gate.js";

// Wide enough for either language's words.
const ICON_ONLY = 34, WITH_COUNT = 100, WITH_TEXT = 150;

// Once for each time Herdr opens, however often a layout brings this copy back:
// someone who stopped the gate by hand should find it stopped.
let autostartTried = false;

export default class Launcher {
  init(_p, cx) {
    this.hot = false;
    this.width = null;
    this.announced = null;
    this.online = 0; // devices connected now, as the gate last wrote it
    this.watching = false;
    this.gateSeen = null;
    if (!autostartTried) {
      autostartTried = true;
      this.startWhenReady(cx, 0);
    }
    // Asking the gate program about itself is how the mod learns the system's
    // language, for someone who has not chosen one; until it answers, English.
    cx.spawn(async (cx) => {
      await gateInfo().catch(() => null);
      cx.notify();
    });
    this.count(cx);
    // The gate's folder is watched, so this is only the fallback, and the
    // retry while that folder is not there yet. A small file, rarely.
    cx.timer.every(15000, (cx) => this.count(cx));
  }

  /** Reads how many devices are connected, and from then on again whenever
   *  the gate writes its connections down. */
  count(cx) {
    cx.spawn(async (cx) => {
      const online = await onlineDevices();
      if (online !== this.online) {
        this.online = online;
        cx.notify();
      }
      if (this.watching) return;
      this.watching = true;
      for (;;) {
        try {
          await watchFolder(gateDir());
        } catch {
          this.watching = false; // no folder yet: the timer tries again
          return;
        }
        const now = await onlineDevices();
        if (now !== this.online) {
          this.online = now;
          cx.notify();
        }
      }
    });
  }

  /** Starts the gate if the person asked for that and it isn't running, once
   *  Herdr knows what runs on this computer (asked each second, for a minute). */
  startWhenReady(cx, tries) {
    cx.timer.after(1000, (cx) => {
      if (!ready()) {
        if (tries < 60) this.startWhenReady(cx, tries + 1);
        return;
      }
      if (!autostart() || gateTerminal()) return;
      cx.spawn(async () => {
        // No program yet: the panel offers the download, there is nothing to report.
        if (await programMissing()) return;
        try {
          await startGate();
        } catch (e) {
          notify({ title: tr(`${NAME} could not start by itself`, `${NAME} 没能自动启动`), body: String(e?.message ?? e), level: "error" });
        }
      });
    });
  }

  /** Fits the block to what's shown, once per change. */
  fit(cx, want) {
    const block = props()?.block;
    if (!block || want === this.width) return;
    this.width = want;
    cx.spawn(async () => { try { await run("layout.resize", { id: block, px: want }); } catch {} });
  }

  render(cx) {
    const t = theme();
    const gate = gateTerminal();
    const waiting = pending(gate);
    // What a stopped gate last wrote about its connections is no longer true.
    const online = gate ? this.online : 0;
    const text = waiting ? tr("Approval needed", "有设备等待批准") : online ? tr(`${online} online`, `${online} 在线`) : null;
    this.fit(cx, waiting ? WITH_TEXT : online ? WITH_COUNT : ICON_ONLY);
    // A gate that has just started has written a fresh, empty list: read it
    // now rather than at the next change.
    if ((gate?.address ?? null) !== this.gateSeen) {
      this.gateSeen = gate?.address ?? null;
      this.count(cx);
    }
    // This copy stays in the layout, so it is the one that speaks up, once
    // for each new question: the gate has no pane, and Herdr rings only for
    // terminals that have one.
    const question = waiting ? `${waiting.title}\n${waiting.message}` : null;
    if (question !== this.announced) {
      this.announced = question;
      if (waiting) cx.spawn(async () => { try { announce(waiting); } catch {} });
    }
    // Green while it runs, dim while it doesn't.
    const color = waiting ? t.blocked : gate ? t.ansi2 : t.dim;
    const label = text && div().text_color(waiting ? t.blocked : this.hot ? t.foreground : t.muted).child(text);
    const button = h_flex().id("herdr-gate-launcher").h_full().px(10).gap(6).items_center().cursor_pointer()
      .bg(this.hot ? t.hover : t.chrome)
      .on_hover((on, cx) => { this.hot = on; cx.notify(); })
      .on_click(() => run("mod.open", { name: NAME }))
      .child(svg(ICON).size(14).text_color(color))
      .when(Boolean(label), (el) => el.child(waiting ? label.loop("opacity", { from: 0.35, to: 1, period: 900 }) : label));
    const font = textFont();
    return h_flex().size_full().justify_end().bg(t.chrome).border_t(1).border_color(t.border).text_size(11.8).when(Boolean(font), (el) => el.font_family(font)).child(button);
  }
}
