# Sequence: CLI dispatch via the urfave command tree

How an `ark` invocation flows through the `urfave/cli` v3 tree — global
flags, routing to a handler `Action`, the DSL custom-parse path, error/
exit handling, help generation, and the transitional legacy catch-all.
Complements seq-cli-dispatch.md, which still owns the proxy-vs-cold-start
decision *inside* a command body (unchanged by this migration).

**Refs:** crc-CLITree.md, crc-CLI.md

## Participants
- main: process entry (`cmd/ark/main.go`)
- root: the `ark` `*cli.Command` (CLITree)
- node: the matched command/subcommand `*cli.Command`
- body: the existing handler logic (crc-CLI.md)

## 1. Main dispatch (declared-flag command)

```
1.1  main: args = cli.ExpandVerbosityFlags(os.Args)   # bundled -vvv → -v -v -v
1.2  main: [transition] pre-parse --dir/-v → arkDir, verbosity, filtered (state-A loop, unchanged)
1.3  main: if cmd not in `migrated` set → legacyDispatch(cmd, args) and return (see 5)
1.4  main: root = BuildRoot(); root.Run(ctx, ["ark"]+filtered)
       # end state: root carries --dir/-v global flags + Before hook; pre-parse dropped
1.5  root: route to the named node (end state: parse globals, Before sets arkDir/verbosity)
1.6  node: parse this node's declared flags (single-or-double dash, stdlib flag)
1.7  node: Action reads c.Bool/String/Int/StringSlice/Count + c.Args().Slice()
1.8  node: Action calls body(...) with those values
1.9  body: server detect / tmp:// / proxy-or-cold-start (seq-cli-dispatch.md), output
1.10 body: on failure → fatal() prints "error: <msg>", os.Exit(1); meaningful os.Exit(2) as today
```

## 2. DSL command (search — SkipFlagParsing)

```
2.1  root: route to the `search` node (SkipFlagParsing: true)
2.2  node: Action receives ALL raw args via c.Args().Slice() — urfave parses none
2.3  node: hand raw args to parseFilterStack (sticky -with/-without, (polarity,mode,query) tuples)
2.4  node: handler's own flag.Parse consumes the non-DSL flags (-k, -scores, …)
2.5  node: if -parse → print disambiguated command, exit without searching
2.6  node: else run the search body unchanged (seq-filter-stack.md / seq-search.md)
```

## 3. Help generation (no hand-maintained printer)

```
3.1  root: -h/--help at any node intercepted natively by urfave
3.2  root: render NAME / USAGE from the node's Name + one-line Usage, with full command path
3.3  root: render COMMANDS — auto-list child nodes' synopses (parent composes children)
3.4  root: render OPTIONS — derive from the node's flag declarations' Usage strings
3.5  node[DSL]: render the single hand-written Description instead of derived flag help
```

## 4. Error from urfave itself

```
4.1  root: flag-parse failure / unknown flag returns an error up the tree
4.2  root: ExitErrHandler renders "error: <msg>" to stderr (fatal() shape),
      then os.Exit(ExitCoder code, else 1) — must exit itself or the code is lost
```

## 5. Legacy catch-all (transitional, in main())

```
5.1  main: cmd name not in the `migrated` set (checked before urfave parses anything)
5.2  main: legacyDispatch(cmd, args)  # the extracted hand-rolled switch
5.3  legacyDispatch: run the un-migrated cmd* handler exactly as before
5.4  (as each group migrates, its case leaves legacyDispatch and joins `migrated`;
      when legacyDispatch is empty, it and the main-level catch-all are deleted)
```
