# `ark claude`: Claude Code hosted by the ark server

**Problem.** An `ark claude` subcommand would start Claude Code as a pty session
hosted by the ark server and attach the current terminal to it. The session
would outlive the terminal that started it, and could be detached from and
re-attached to later, as with tmux.

Opened 2026-10-29, moved out of [windows-support.md](windows-support.md) (its
Item 12) by Bill: the Windows reason for it had gone, and what was left is a
feature, not Windows support.

**Where it came from.** DECIDED (Bill, 2026-10-13), in windows-support: on
Windows this was to be the general way past the ssh junction problem. A hosted
session is a child of the server, so when the server runs outside the ssh
session (as a scheduled task), Claude Code inherits that context and reads the
`~/.claude` junctions, while the user drives it from ssh through the attach
path the Windows pty work had already proved. Two things removed that motive the
same day. A default standard user turned out to be unable to start a task from
ssh at all, and then the level-set: real users run from the desktop, which does
not enforce the junction policy, and ssh is only our test harness. It was parked
then, and the off-Windows value is what remains.

It is an explicit command, so it stays inside the rule that ark never starts
Claude on its own.

## Status

- [ ] **Item 1 — decide what it is, off Windows.** **OPEN (not queued.)** The
      open questions below; nothing is built. See Item 1.

## Parts

**Item 1.** Grounded 2026-10-13: the server holds exactly one `PtyHost`
(`server.go:100`, made at `:349`), and it is the Luhmann seat, so this needs
hosts per session, not a new mode on the existing one. (Line numbers from that
date.)

Open questions:
- **What it is for** off Windows: a persistent, re-attachable session (tmux-like)
  rather than a workaround. Whether that earns its place beside tmux itself is
  the first question.
- One session per invocation, or named sessions a user can detach from and
  re-attach to later.
- How it carries the caller's working directory, environment and arguments into
  the hosted `claude`.
- Whether `ark claude` starts the server itself when it finds none. (In
  windows-support this was "as a task, when the session enforces
  RedirectionTrust"; that case is gone.)
