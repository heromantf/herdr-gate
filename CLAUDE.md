# Working on herdr-gate

Two Claude Code sessions work on this repository, one on macOS and one on Windows. Each
can run only its own system, and both push straight to `main`: no branches, and no
version bump unless asked.

## Changes to code both systems run

Once the mod is listed on herdr.dev, `main` is what people get. Herdr's guide has an
agent copy `mods/herdr-gate/` from the repository as it is at that moment; nothing in it
names a tag or a release. (The gate program is different: it comes from the newest
release, and changes only with a new tag.) So a change that one side pushed and the
other never ran can reach people.

For a change to shared code:

- Say in the commit message that it touches shared code, and which system has not run
  it yet.
- The other side pulls and verifies that change before anything else, by running it,
  and reports what it ran and what it saw.
- Until then neither side pushes another change to shared code on top of it.

Shared code is every Go file without a build constraint, and everything in
`mods/herdr-gate/` that is not inside a branch on the system (`os`, `platform()`).

Code only one system runs (`run_windows.go`, `lang_darwin.go`, such a branch in
`gate.js`) needs no wait: verify it where it runs, then push.

Do not report something as passing that was not run. Say what could not be run here,
and leave it to the other side.
