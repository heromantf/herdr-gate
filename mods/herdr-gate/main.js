// herdr-gate for any Herdr layout: starts and stops the gate program that
// lets other devices reach this computer, and answers its "may this device
// connect?" question. One mod in two roles:
//   (default)  the panel: a dialog (mod.open herdr-gate)
//   launcher   a status-bar button that shows the gate's state and opens the panel
// The gate itself is a program beside this file (bin/): it runs in a terminal
// that herdr-core keeps, so it outlives this window.

import { View } from "gpui-kit";
import { props } from "herdr";
import Panel from "./panel.js";
import Launcher from "./launcher.js";

export default class HerdrGate extends View {
  init(p, cx) {
    const pr = props() ?? p ?? {};
    this.role = pr.role === "launcher" ? new Launcher() : new Panel();
    this.role.init(pr, cx);
  }

  render(cx) {
    return this.role.render(cx);
  }
}
