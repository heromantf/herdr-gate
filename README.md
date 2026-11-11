# herdr-gate

[简体中文](README.zh-CN.md)

A small SSH server for your own machines. Run it on a machine you want to reach, and
herdr's desktop app and phone app connect to that machine as an ordinary SSH host, without
turning on the system's sshd.

It accepts public keys only, and runs the command the client sends the way the system
would: your login shell on macOS and Linux, cmd on Windows. It offers no interactive
terminal, no file transfer and no port forwarding.

A device you approve can run any command on this machine as you. Approve only your own.

## Run it

In a terminal on the machine to be reached. It runs as long as that window stays open:

```sh
herdr-gate                # port 7522 on every interface
herdr-gate -listen :2200  # another port
```

It prints this machine's addresses and fingerprint. On Windows the firewall asks once,
the first time: allow it.

The address Tailscale gave this machine comes first, and the mod's panel marks it. Which
one that is, herdr-gate asks the `tailscale` command; where that is not installed or not
signed in, no address is marked.

hc must be installed on the machine, and `hc` must run from that terminal.

## Approve a device

The first time a device connects, the window shows where it connects from and its key's
fingerprint, and asks. Type `y` and Enter to approve. Enter alone refuses; a refused
device is not asked about again until herdr-gate restarts.

### Your own Tailscale devices, without asking

herdr-gate can approve a new device without the question when Tailscale says it is
signed in with the same account as this machine. This is off until you switch it on, in
the mod's panel or with:

```sh
herdr-gate -auto-approve on    # or off
```

A running herdr-gate follows the switch at once. For each new device it asks the
`tailscale` command about both ends of the connection: it has to arrive on this
machine's own Tailscale address, from a device of the same account. Everything else is
still asked about: other people's devices on your tailnet, tagged devices, and whatever
reaches you through another device's routes. A device you refused stays refused until
herdr-gate restarts.

A device approved this way is on the list like any other, with its name in Tailscale,
and is revoked the same way.

With the switch on, signing a device in to your Tailscale account is all it takes for it
to run commands on this machine.

## Connect from another device

- Desktop app: in Add machine, enter `user@address:7522`. A computer has to accept this
  machine's fingerprint the first time: on macOS and Linux run
  `hc machine add user@address:7522` in a terminal; on Windows run
  `ssh -p 7522 user@address` once.
- Phone app: add a machine with the address, port 7522 and the user name.

The user name must be the system account herdr-gate runs as, in any letter case. The
startup lines and `-info` show it. A login under another name is refused without asking.

The phone app waits for its login a shorter time than herdr-gate waits for your answer
(about 30 seconds in version 1.0.5). If the phone says the sign-in timed out first, the
question is still in the window: approve it, then connect from the phone once more. It
is not asked again.

When the phone tells you to add its public key by hand, do not use the command it
offers: that one writes `~/.ssh/authorized_keys`, which herdr-gate does not read.
Approve the phone in herdr-gate's window or panel; if it was refused before, add the
public key the phone shows as one line to herdr-gate's own `authorized_keys` (see Files).

## Run it in a herdr terminal

You can also open a terminal tab in herdr-app on that machine and run herdr-gate there.
It keeps running when the app's window closes. Closing the tab ends it.

There, when a new device waits for approval, herdr-gate says so in herdr's status
protocol (OSC 7501) and withdraws it once answered, so herdr shows it as an agent that
needs you.

To answer from a command, add `--force`: herdr does not type into a program that is
waiting unless told that this is the answer.

```sh
hc send --force <terminal> y
```

## The herdr-app mod

`mods/herdr-gate/` is a mod for herdr-app that starts and stops herdr-gate and approves
devices from inside the app. It only calls `hc` and the herdr-gate program.

The mod runs herdr-gate in the background, with no tab of its own: closing a tab in
herdr-app ends the terminal in it, so the gate must not live in one. Without a tab
herdr's own attention rules pass it by, so the status-bar button posts the notification.

The panel shows:

- whether the gate runs, with Start and Stop;
- what other devices should enter to connect, one line for each address, with Copy;
- this machine's fingerprint;
- the port, which you can change;
- a switch to start the gate whenever Herdr opens;
- a switch to approve your own Tailscale devices without asking;
- the language;
- the approved devices, whether each is connected now, and Revoke;
- the gate's log, read-only.

The status-bar button is green while the gate runs, shows how many devices are connected,
and says when one waits for approval.

Install: run `go run build.go`, which puts the programs in the mod's `bin/`, then copy
`mods/herdr-gate/` to `~/.config/herdr-app/mods/herdr-gate/`.

To update: stop herdr-gate first, and delete the old `~/.config/herdr-app/mods/herdr-gate/`
before copying the new one. On a Mac with Apple silicon, a program copied over the one
that is running is ended by the system the moment it starts, and stays that way until
the file is deleted and copied again.

- The panel: `herdr-app do mod.open --name herdr-gate`.
- The status-bar button: add a block to a layout of your own. Notifications, the switch
  to start with Herdr, and the count all depend on it.

```json
{ "id": "herdr-gate", "type": "block", "size": { "px": 34 },
  "content": { "mod": "herdr-gate", "props": { "role": "launcher", "block": "herdr-gate" } } }
```

## Language

herdr-gate and the mod speak English or Chinese. They follow the system's language, and
use English when it is neither.

- In the mod's panel, Language chooses between the system's, English and 中文. The panel
  and the status-bar button change at once; a running gate's own log and approval
  questions change when it is next started.
- Run by hand: `herdr-gate -lang zh` (or `en`). Where no `-lang` is given, the
  environment variable `HERDR_GATE_LANG` does the same.

## Files

In `~/.config/herdr-gate/` (change it with `-dir` or `HERDR_GATE_HOME`):

- `authorized_keys`: the approved devices, one per line. Deleting a line revokes that
  device: it can no longer log in, and a connection it has is closed within seconds.
- `host_key`: the key behind this machine's fingerprint. Delete it and the next start
  makes a new one; every client then has to accept the new fingerprint.
- `connections.json`: who is connected now, rewritten by a running gate each time a
  device connects or goes.
- `auto_approve_tailscale`: there while your own Tailscale devices are approved without
  asking. Deleting it switches that off.

## Ask and revoke

```sh
herdr-gate -info                 # account, addresses, fingerprint, devices, connections (JSON)
herdr-gate -revoke SHA256:...    # revoke a device, by its fingerprint or its key from -info
```

Neither needs herdr-gate to be running. The mod's panel uses both.

## Build and test

Go 1.26 or later.

```sh
go test ./...
go run build.go   # the program for every system, into mods/herdr-gate/bin/
go build .        # this system's only, into this folder
```

`build.go` makes `herdr-gate-<os>-<arch>` (with `.exe` on Windows) for Windows, macOS and
Linux. The mod picks the one for the machine it runs on.

## License

[MIT](LICENSE)
