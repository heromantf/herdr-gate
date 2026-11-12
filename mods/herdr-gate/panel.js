// The herdr-gate panel (mod.open herdr-gate): whether the gate runs, the
// buttons that start and stop it, how other devices reach this computer, the
// device waiting for approval if there is one, and the devices already
// approved, each with a way to take its approval back.

import { div, svg } from "gpui-kit";
import { v_flex, h_flex, InputState, Input, Scrollbar, v_virtual_list, VirtualListScrollHandle } from "gpui-base";
import { theme, props, closeOverlay, settings, watchFolder } from "herdr";
import {
  NAME, ICON, DEFAULT_PORT, tr, textFont, gateTerminal, pending, gateInfo, gateLog, revokeDevice, startGate, stopGate, answer,
  autostart, setAutostart, port, setPort, parsePort, runningPort,
  language, languageChoice, setLanguageChoice, runningLanguage, chineseFont, setAutoApprove,
  programMissing, downloadGate, downloadProgress, programInfo, shortPath, learnSystemLanguage,
} from "./gate.js";

// Herdr's own close glyph, shipped with every install.
const CLOSE = "mods/workspace-chrome/x.svg";
// The height of one line of the log, px.
const LOG_ROW = 22;

/**
 * About how wide the longest of these lines draws at 12px, in px: a script
 * cannot measure text, and the scroll area only scrolls sideways over content
 * it is told is wider than itself. Chinese characters are a full em, the rest
 * a little over half. Erring long leaves some empty room at the end.
 */
function textWidth(lines) {
  let widest = 0;
  for (const line of lines) {
    let width = 0;
    for (const ch of line) width += ch.codePointAt(0) > 0x2e7f ? 12 : 7;
    widest = Math.max(widest, width);
  }
  return widest;
}

export default class Panel {
  init(_p, cx) {
    this.busy = null; // "start" | "stop" | "restart" | "answer" | "revoke"
    this.error = null;
    this.info = null; // what the gate program says about this computer
    this.copied = null;
    this.revoking = null; // the device whose "really?" is showing
    this.page = "main"; // or "log"
    this.log = null; // the gate's last lines, oldest first
    this.logScroll = VirtualListScrollHandle.new();
    this.handle = cx.focus_handle();
    this.handle.focus();
    this.typedPort = String(port());
    this.portInput = InputState.new({ placeholder: String(DEFAULT_PORT) });
    this.portInput.set_value(this.typedPort);
    this.portInput.on("change", (_e, cx) => {
      this.typedPort = this.portInput.value();
      const n = parsePort(this.typedPort);
      if (n && n !== port()) setPort(n);
      cx.notify();
    });
    this.program = null; // what is known about the gate program itself
    this.progress = null; // how far a download is, while one runs
    this.reload(cx);
    this.loadProgram(cx);
    // The device list changes when someone is approved; the panel is open briefly.
    cx.timer.every(5000, (cx) => this.reload(cx));
    cx.timer.every(2000, (cx) => { if (this.page === "log") this.loadLog(cx); });
    cx.timer.every(1000, (cx) => { if (this.busy === "download") this.loadProgress(cx); });
  }

  /** Turns to the log page, at its newest line, every time. */
  openLog(cx) {
    this.page = "log";
    this.logToEnd = true;
    this.logEntered = true; // the next load goes to the end even if it brings nothing new
    this.loadLog(cx);
    // And once more when the list has been laid out: one that has only just
    // appeared has no length yet to scroll to the end of.
    cx.timer.after(150, (cx) => { if (this.page === "log") { this.logToEnd = true; cx.notify(); } });
    cx.notify();
  }

  loadLog(cx) {
    const gate = gateTerminal();
    cx.spawn(async (cx) => {
      let log;
      try {
        log = gate ? await gateLog(gate) : [];
      } catch (e) {
        log = [String(e?.message ?? e)];
      }
      // To the newest line when there is something new, and only then:
      // someone reading further up is left where they are.
      const changed = !this.log || log.length !== this.log.length || log[log.length - 1] !== this.log[this.log.length - 1];
      this.log = log;
      if (changed || this.logEntered) this.logToEnd = true; // done while drawing, when the list has its new length
      this.logEntered = false;
      cx.notify();
    });
  }

  reload(cx) {
    cx.spawn(async (cx) => {
      await this.read();
      this.watch(cx);
      cx.notify();
    });
  }

  /**
   * Reads again the moment the gate's folder changes: a device approved,
   * revoked, connected or gone. The timer in init stays as the fallback, and
   * as the retry when the folder is not there yet.
   */
  watch(cx) {
    const dir = this.info?.dir;
    if (!dir || this.watching) return;
    this.watching = true;
    cx.spawn(async (cx) => {
      for (;;) {
        try {
          await watchFolder(dir);
        } catch {
          this.watching = false;
          return;
        }
        await this.read();
        cx.notify();
      }
    });
  }

  /** Reads how far the download is, for the line beside the button. */
  loadProgress(cx) {
    cx.spawn(async (cx) => {
      const progress = await downloadProgress().catch(() => null);
      if (this.busy !== "download") return; // it ended meanwhile
      this.progress = progress;
      cx.notify();
    });
  }

  /** Looks at the gate program itself: its file name, and where it lies. */
  loadProgram(cx) {
    cx.spawn(async (cx) => {
      this.program = await programInfo().catch(() => null);
      cx.notify();
    });
  }

  async read() {
    // A copy of the mod taken from the repository has no gate program yet:
    // then there is nothing to ask, only the download to offer.
    this.missing = await programMissing();
    if (this.missing) {
      // Nothing to ask which language the system speaks: find out without it.
      await learnSystemLanguage();
      return;
    }
    try {
      this.info = await gateInfo();
    } catch (e) {
      if (!this.info) this.error = String(e?.message ?? e);
    }
  }

  /** One call at a time; what went wrong is shown, not thrown. */
  act(cx, what, work) {
    if (this.busy) return;
    this.busy = what;
    this.error = null;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        await work(cx);
      } catch (e) {
        this.error = String(e?.message ?? e);
      }
      this.busy = null;
      this.progress = null;
      await this.read();
      this.program = await programInfo().catch(() => null);
      cx.notify();
    });
  }

  close() {
    closeOverlay(props().overlay);
  }

  button(t, id, label, onClick, primary) {
    return div()
      .id(id)
      .px(14)
      .h(30)
      .flex()
      .flex_none()
      .items_center()
      .rounded(7)
      .border_1()
      .border_color(primary ? t.solid : t.input)
      .bg(primary ? t.solid : t.raised)
      .text_color(primary ? t.solid_foreground : t.foreground)
      .text_size(13)
      .when(Boolean(this.busy), (el) => el.opacity(0.5))
      .cursor_pointer()
      .hover((s) => s.opacity(0.88))
      .on_click(onClick)
      .child(label);
  }

  /** A small button inside a row. */
  link(t, id, label, onClick, color) {
    return div().id(id).px(8).py(3).flex_none().rounded(6).text_size(12).text_color(color ?? t.muted).cursor_pointer().hover((s) => s.bg(t.hover).text_color(color ?? t.foreground)).on_click(onClick).child(label);
  }

  toggle(t, id, on, onClick) {
    return div()
      .id(id)
      .w(36)
      .h(20)
      .flex_none()
      .rounded(10)
      .p(2)
      .cursor_pointer()
      .bg(on ? t.accent : t.hover)
      .flex()
      .when(on, (el) => el.justify_end())
      .on_click(onClick)
      .child(div().w(16).h(16).rounded(8).bg(on ? t.accent_foreground : t.muted));
  }

  /** One out of a few, side by side, the chosen one filled in. An option is
   *  [value, label] and, for a label Herdr's own font draws poorly, a font. */
  choice(t, id, options, chosen, onPick) {
    return h_flex()
      .flex_none()
      .h(28)
      .p(2)
      .gap(2)
      .items_center()
      .rounded(7)
      .border_1()
      .border_color(t.input)
      .bg(t.background)
      .children(
        options.map(([value, label, font]) => {
          const on = value === chosen;
          return div()
            .id(`${id}-${value}`)
            .px(10)
            .h(22)
            .flex()
            .items_center()
            .rounded(5)
            .text_size(12)
            .text_color(on ? t.accent_foreground : t.muted)
            .when(on, (el) => el.bg(t.accent))
            .when(!on, (el) => el.cursor_pointer().hover((s) => s.bg(t.hover).text_color(t.foreground)))
            .when(Boolean(font), (el) => el.font_family(font))
            .on_click(() => { if (!on) onPick(value); })
            .child(label);
        }),
      );
  }

  heading(t, text) {
    return div().pt(18).pb(6).text_size(12).text_color(t.dim).child(text);
  }

  /** One setting: what it is on the left, the control on the right. */
  setting(t, title, about, control) {
    return h_flex()
      .pt(12)
      .gap(12)
      .items_center()
      .child(
        v_flex()
          .flex_1()
          .min_w(0)
          .gap(2)
          .child(div().text_size(13).text_color(t.foreground).child(title))
          .when(Boolean(about), (el) => el.child(div().text_size(12).line_height("18px").text_color(t.dim).child(about))),
      )
      .child(control);
  }

  /** The device at the door: what the gate reported, and the two answers. */
  question(t, gate, waiting) {
    return v_flex()
      .mt(14)
      .p(12)
      .gap(8)
      .rounded(10)
      .bg(t.blocked + "14")
      .border_1()
      .border_color(t.blocked + "40")
      .child(div().text_size(13).font_weight(600).text_color(t.foreground).child(waiting.title))
      .child(div().text_size(12).line_height("18px").text_color(t.muted).child(waiting.message))
      .child(
        h_flex()
          .gap(8)
          .child(this.button(t, "hg-allow", tr("Allow", "允许"), (_e, cx) => this.act(cx, "answer", () => answer(gate, true)), true))
          .child(this.button(t, "hg-deny", tr("Deny", "拒绝"), (_e, cx) => this.act(cx, "answer", () => answer(gate, false)))),
      );
  }

  /** What to type on another device, one line for each of this computer's addresses. */
  addresses(t, shownPort) {
    const info = this.info;
    if (!info) return [div().px(8).py(6).text_size(12).text_color(t.dim).child(tr("Loading…", "正在读取…"))];
    if (!info.addresses.length) return [div().px(8).py(6).text_size(12).text_color(t.dim).child(tr("No network address available", "没有可用的网络地址"))];
    return info.addresses.slice(0, 4).map((address, i) => {
      const text = `${info.user}@${address}:${shownPort}`;
      return h_flex()
        .id(`hg-address-${i}`)
        .h(30)
        .px(8)
        .gap(8)
        .items_center()
        .rounded(7)
        .hover((s) => s.bg(t.hover))
        .child(div().flex_none().text_size(13).text_color(t.foreground).child(text))
        .child(div().flex_1().min_w(0).truncate().text_size(12).text_color(t.dim).child(info.tailscale.includes(address) ? "Tailscale" : ""))
        .child(this.link(t, `hg-copy-${i}`, this.copied === text ? tr("Copied", "已复制") : tr("Copy", "复制"), (_e, cx) => { cx.write_to_clipboard(text); this.copied = text; cx.notify(); }));
    });
  }

  /** One approved device: where it is, whether it is connected now, and the way to revoke it. */
  device(t, d, i, connections) {
    const live = connections.filter((c) => c.key === d.key);
    // Connected: where it connects from now. Otherwise: where it was approved from.
    const where = live[0]?.from || d.from || d.note || d.type;
    // One that was approved without the question goes by its name in Tailscale.
    const what = d.name ? `${d.name} · ${where}` : where;
    const state = !live.length ? tr("offline", "离线") : live.length > 1 ? tr(`online · ${live.length} connections`, `在线 · ${live.length} 个连接`) : tr("online", "在线");
    const row = h_flex()
      .id(`hg-device-${i}`)
      .h(30)
      .px(8)
      .gap(8)
      .items_center()
      .rounded(7)
      .hover((s) => s.bg(t.hover))
      .child(div().w(6).h(6).flex_none().rounded(3).bg(live.length ? t.ansi2 : t.dim))
      .child(div().flex_none().text_size(13).text_color(t.foreground).child(what))
      .child(div().flex_none().text_size(12).text_color(live.length ? t.ansi2 : t.dim).child(state))
      .child(div().flex_1().min_w(0).truncate().text_size(12).text_color(t.dim).child(d.fingerprint.replace(/^SHA256:/, "").slice(0, 10)));
    if (this.revoking !== d.key) return row.child(this.link(t, `hg-revoke-${i}`, tr("Revoke", "撤销"), (_e, cx) => { this.revoking = d.key; cx.notify(); }));
    // Asked twice: it cuts off a device that may be in use right now.
    return v_flex()
      .child(row)
      .child(
        h_flex()
          .px(8)
          .pb(6)
          .gap(8)
          .items_center()
          .child(div().flex_1().min_w(0).text_size(12).line_height("18px").text_color(t.muted).child(tr("It is disconnected, and has to be approved again to reconnect.", "撤销后它会断开，再连需重新批准。")))
          .child(this.link(t, `hg-revoke-yes-${i}`, tr("Revoke", "撤销"), (_e, cx) => { this.revoking = null; this.act(cx, "revoke", () => revokeDevice(d)); }, t.blocked))
          .child(this.link(t, `hg-revoke-no-${i}`, tr("Cancel", "取消"), (_e, cx) => { this.revoking = null; cx.notify(); })),
      );
  }

  /** The gate's own window as text, in the order it was written: newest at the bottom. */
  logPage(t, font, gate) {
    const back = (cx) => { this.page = "main"; cx.notify(); };
    const text = this.log === null ? [tr("Loading…", "正在读取…")] : this.log.length ? this.log : [gate ? tr("Nothing yet", "还没有内容") : tr("Not running", "未运行")];
    // An empty row at the end keeps the last line clear of the sideways bar
    // that lies over the bottom.
    const lines = [...text, ""];
    if (this.logToEnd) {
      this.logToEnd = false;
      this.logScroll.scroll_to_bottom();
    }
    return v_flex()
      .id("herdr-gate-log")
      .track_focus(this.handle)
      .on_key_down((e, cx) => { if (e.key === "escape") { back(cx); cx.stop_propagation(); } })
      .w_full()
      // As tall as the page it came from, so the dialog does not jump.
      .h(this.mainHeight ?? 560)
      .p(18)
      .bg(t.popover)
      .text_color(t.foreground)
      .when(Boolean(font), (el) => el.font_family(font))
      .child(
        h_flex()
          .flex_none()
          .gap(4)
          .items_center()
          .child(svg(ICON).size(16).text_color(t.muted))
          .child(div().pl(4).flex_1().text_size(15).font_weight(600).child(tr(`${NAME} log`, `${NAME} 日志`)))
          .child(this.link(t, "hg-back", tr("Back", "返回"), (_e, cx) => back(cx)))
          .child(this.closeButton(t)),
      )
      .child(
        div()
          .relative()
          .flex_1()
          .min_h(0)
          .mt(12)
          .pt(6)
          .rounded(8)
          .bg(t.background)
          .border_1()
          .border_color(t.border)
          // One line each, never wrapped: a long one is reached by scrolling
          // sideways. Built as Herdr's own Changes panel builds its diff: a
          // list that can be sent to its end from code (see loadLog), inside
          // an area that scrolls sideways over the widest line.
          .child(
            div()
              .id("hg-log-x")
              .size_full()
              .overflow_x_scrollbar()
              .mode("always")
              .child(
                div()
                  .w_full()
                  .min_w(textWidth(lines) + 36)
                  .h_full()
                  .child(
                    v_virtual_list("hg-log", lines.length, LOG_ROW, (i) => String(i), (range) =>
                      lines.slice(range.start, range.end).map((line) => div().h(LOG_ROW).px(10).flex().items_center().whitespace_nowrap().text_size(12).text_color(t.muted).child(line)),
                    )
                      .track_scroll(this.logScroll)
                      .size_full(),
                  ),
              ),
          )
          .child(Scrollbar.vertical("hg-log").mode("always").viewport_from_layout().absolute().inset_0()),
      );
  }

  /** The gate program in use: its version, and where it lies. */
  programRow(t) {
    const program = this.program;
    const version = this.info?.version;
    return v_flex()
      .pt(12)
      .gap(2)
      .child(
        h_flex()
          .gap(12)
          .items_center()
          .child(div().flex_1().min_w(0).text_size(13).text_color(t.foreground).child(tr("Program", "程序")))
          .child(div().flex_none().text_size(13).text_color(t.foreground).child(version ? `${NAME} ${version}` : NAME)),
      )
      .when(Boolean(program?.path), (el) => el.child(div().truncate().text_size(12).line_height("18px").text_color(t.dim).child(shortPath(program.path))));
  }

  /** What the line beside the Download button says while it downloads: how
   *  far it is and how fast, once curl has begun to tell. */
  downloading() {
    const p = this.progress;
    if (!p) return tr("Downloading…", "正在下载…");
    return tr(`Downloading… ${p.percent}% · ${p.received} of ${p.total} · ${p.speed}`, `正在下载… ${p.percent}% · ${p.received} / ${p.total} · ${p.speed}`);
  }

  /** All there is to show while this copy of the mod has no gate program for
   *  this computer: that, the button that downloads one, and where to put
   *  one by hand. */
  missingPage(t, font) {
    const program = this.program;
    return v_flex()
      .id("herdr-gate-panel")
      .track_focus(this.handle)
      .on_key_down((e, cx) => { if (e.key === "escape") { this.close(); cx.stop_propagation(); } })
      .w_full()
      .min_h(0)
      .p(18)
      .bg(t.popover)
      .text_color(t.foreground)
      .when(Boolean(font), (el) => el.font_family(font))
      .child(
        h_flex()
          .gap(4)
          .items_center()
          .child(svg(ICON).size(16).text_color(t.muted))
          .child(div().pl(4).flex_1().text_size(15).font_weight(600).child(NAME))
          .child(this.closeButton(t)),
      )
      .child(div().pt(6).text_size(13).line_height("20px").text_color(t.muted).child(tr("Lets your other devices reach this computer over SSH.", "让其他设备通过 SSH 连到这台电脑。")))
      .child(div().pt(14).text_size(13).text_color(t.foreground).child(tr("The herdr-gate program for this computer is not here yet.", "这台电脑上还没有 herdr-gate 程序。")))
      .child(div().pt(4).text_size(12).line_height("18px").text_color(t.dim).child(tr("Download gets the newest release's from GitHub.", "点下载会从 GitHub 取最新发布的那个。")))
      .child(
        h_flex()
          .pt(14)
          .gap(8)
          .items_center()
          .child(this.button(t, "hg-download", tr("Download", "下载"), (_e, cx) => this.act(cx, "download", () => downloadGate()), true))
          .when(this.busy === "download", (el) => el.child(div().text_size(12).text_color(t.muted).child(this.downloading()))),
      )
      .when(Boolean(this.error), (el) => el.child(div().pt(8).text_size(12).line_height("18px").text_color(t.blocked).child(String(this.error))))
      // One from elsewhere is as good: built from the source, or fetched by hand.
      .when(Boolean(program), (el) =>
        el.child(div().pt(14).text_size(12).line_height("18px").text_color(t.dim).child(tr(`Or put ${program.file} into ${shortPath(program.bin)} yourself.`, `也可以自己把 ${program.file} 放进 ${shortPath(program.bin)}。`))),
      );
  }

  closeButton(t) {
    return div().id("hg-close").p(4).flex_none().rounded(5).text_color(t.dim).cursor_pointer().hover((s) => s.bg(t.hover).text_color(t.foreground)).on_click(() => this.close()).child(svg(CLOSE).size(14));
  }

  render() {
    const t = theme();
    const font = textFont();
    const gate = gateTerminal();
    if (this.missing && !gate) return this.missingPage(t, font);
    if (this.page === "log") return this.logPage(t, font, gate);
    const waiting = pending(gate);
    const starts = autostart();
    const wanted = port();
    const running = gate ? runningPort(gate) : wanted;
    // What a stopped gate last wrote about its connections is no longer true.
    const connections = gate ? (this.info?.connections ?? []) : [];
    const online = new Set(connections.map((c) => c.key)).size;
    const state = !gate ? tr("Not running", "未运行") : waiting ? tr("A device is waiting for approval", "有设备等待批准") : online ? tr(`Running · ${online} ${online === 1 ? "device" : "devices"} online`, `运行中 · ${online} 台设备在线`) : tr("Running", "运行中");
    const dot = !gate ? t.dim : waiting ? t.blocked : t.ansi2;
    const working = { start: tr("Starting…", "正在启动…"), stop: tr("Stopping…", "正在停止…"), restart: tr("Restarting…", "正在重新启动…"), answer: tr("Answering…", "正在回答…"), revoke: tr("Revoking…", "正在撤销…"), save: tr("Saving…", "正在保存…") }[this.busy];
    const auto = this.info?.autoApprove === true;
    // Without Tailscale on this computer the switch has nothing to go by.
    const noTailscale = Boolean(this.info) && !this.info.tailscale.length;
    const devices = this.info?.devices ?? [];
    const badPort = parsePort(this.typedPort) === null;
    const restart = (_e, cx) =>
      this.act(cx, "restart", async (cx) => {
        await stopGate(gate);
        // A moment for the system to give the port back.
        await new Promise((done) => cx.timer.after(700, () => done()));
        await startGate();
      });

    const controls = h_flex().pt(14).gap(8).items_center();
    if (gate) {
      controls
        .child(this.button(t, "hg-stop", tr("Stop", "停止"), (_e, cx) => this.act(cx, "stop", () => stopGate(gate))))
        .child(this.button(t, "hg-show", tr("View log", "查看日志"), (_e, cx) => this.openLog(cx)));
    } else {
      controls.child(this.button(t, "hg-start", tr("Start", "启动"), (_e, cx) => this.act(cx, "start", () => startGate()), true));
    }
    if (working) controls.child(div().text_size(12).text_color(t.muted).child(working));

    const portBox = h_flex()
      .gap(8)
      .items_center()
      .flex_none()
      .when(Boolean(gate) && running !== wanted, (el) => el.child(this.link(t, "hg-restart", tr("Restart", "重新启动"), restart, t.accent)))
      .child(Input.new(this.portInput).w(76).h(28).px(8).rounded(7).border_1().border_color(badPort ? t.blocked : t.input).bg(t.background).text_size(13).text_color(t.foreground));

    // A running gate keeps the language it was started with: its log and its
    // questions change at the next start, the panel at once.
    const behind = Boolean(gate) && runningLanguage(gate) !== language();
    const languageBox = h_flex()
      .gap(8)
      .items_center()
      .flex_none()
      .when(behind, (el) => el.child(this.link(t, "hg-restart-language", tr("Restart", "重新启动"), restart, t.accent)))
      .child(this.choice(t, "hg-language", [["auto", tr("System", "跟随系统")], ["en", "English"], ["zh", "中文", chineseFont()]], languageChoice(), (choice) => setLanguageChoice(choice)));

    return v_flex()
      .id("herdr-gate-panel")
      .track_focus(this.handle)
      .on_key_down((e, cx) => { if (e.key === "escape") { this.close(); cx.stop_propagation(); } })
      // Remembered for the log page, which takes the same height. The size
      // comes back with the person's zoom applied; heights are given without it.
      .on_size_change((size) => { this.mainHeight = size.height / (Number(settings()?.zoom) || 1); })
      .w_full()
      .min_h(0)
      .p(18)
      .bg(t.popover)
      .text_color(t.foreground)
      .when(Boolean(font), (el) => el.font_family(font))
      .child(
        h_flex()
          .gap(4)
          .items_center()
          .child(svg(ICON).size(16).text_color(t.muted))
          .child(div().pl(4).flex_1().text_size(15).font_weight(600).child(NAME))
          .child(this.closeButton(t)),
      )
      .child(div().pt(6).text_size(13).line_height("20px").text_color(t.muted).child(tr("Lets your other devices reach this computer over SSH.", "让其他设备通过 SSH 连到这台电脑。")))
      .child(
        h_flex()
          .pt(14)
          .gap(8)
          .items_center()
          .child(div().w(8).h(8).flex_none().rounded(4).bg(dot))
          .child(div().text_size(13).text_color(t.foreground).child(state)),
      )
      .when(Boolean(waiting), (el) => el.child(this.question(t, gate, waiting)))
      .child(controls)
      .when(Boolean(this.error), (el) => el.child(div().pt(8).text_size(12).line_height("18px").text_color(t.blocked).child(String(this.error))))
      .child(this.heading(t, tr("Connect with", "连接地址")))
      .child(v_flex().children(this.addresses(t, running)))
      .when(Boolean(this.info?.fingerprint), (el) => el.child(div().px(8).pt(4).text_size(12).line_height("18px").text_color(t.dim).child(tr(`Fingerprint ${this.info.fingerprint}`, `本机指纹 ${this.info.fingerprint}`))))
      .child(this.setting(t, tr("Port", "端口"), badPort ? tr("1 to 65535", "范围 1–65535") : tr("Takes effect after a restart", "修改后需重新启动"), portBox))
      .child(this.setting(t, tr("Start with Herdr", "随 Herdr 自动启动"), "", this.toggle(t, "hg-autostart", starts, () => setAutostart(!starts))))
      .child(
        this.setting(
          t,
          tr("Auto-approve my Tailscale devices", "自动批准我的 Tailscale 设备"),
          noTailscale ? tr("Tailscale is not connected on this computer", "这台电脑没有连上 Tailscale") : tr("Devices on the same Tailscale account connect without being asked about", "同一个 Tailscale 账号下的设备不再询问"),
          this.toggle(t, "hg-auto-approve", auto, (_e, cx) => this.act(cx, "save", () => setAutoApprove(!auto))),
        ),
      )
      .child(this.setting(t, tr("Language", "语言"), behind ? tr("The log and approval questions follow after a restart", "日志和批准提示重新启动后切换") : "", languageBox))
      .child(this.programRow(t))
      .child(this.heading(t, tr(`Approved devices (${devices.length})`, `已批准的设备（${devices.length}）`)))
      .child(
        div()
          .relative()
          .child(
            v_flex()
              .id("hg-devices")
              .max_h(190)
              .overflow_y_scroll()
              .children(
                devices.length
                  ? devices.map((d, i) => this.device(t, d, i, connections))
                  : [div().px(8).py(6).text_size(12).text_color(t.dim).child(tr("None", "暂无"))],
              ),
          )
          .child(Scrollbar.vertical("hg-devices").absolute().inset_0()),
      );
  }
}
