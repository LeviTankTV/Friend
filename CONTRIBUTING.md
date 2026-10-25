# Contributing

Contributions are welcome — pick something from the roadmap in README.md
or a stage from PLAN.md.

## Ground rules

- **Measurements over vibes.** PLAN.md is the dev diary: every design
  decision and every bug found on a live run is written down there, with
  measurements. When you change behavior (voice, proactive, mood, walking),
  run the live self-test and record what you measured.
- **Privacy stays.** Everything that can work locally must work locally.
  New features must not send data anywhere without an explicit config opt-in.
- Keep the character in character: her lines stay one living line, never
  assistant-speak.

## Getting set up

Requirements: Go 1.24+, Node 18+.

```bash
# Windows: start.bat   Linux: ./start.sh   macOS: ./start.command
# (they check toolchain, build server + helpers, run the app)
```

## Checks before a PR

```bash
cd server && go vet ./... && go test ./...   # 23 packages, all green
cd desktop && npx electron . --selftest      # 30 checks, exit 0
```

CI runs both on Windows/Linux/macOS. The Windows `go vet` warning on the
WASAPI vcall pattern in soundwatch is documented in PLAN.md (vet is clean
on linux/darwin).

Add a test for behavior you change: server-side as a Go test in the owning
package, character behavior as a new numbered self-test part in
`desktop/main.js` (append the flag to the exit gate at the end of the file).

## Localization

Web strings: `web/js/lang/{ru,en}.js`. Server prompts:
`server/internal/i18n/i18n.go` (the test enforces: no empty fields,
identical format verbs across languages). Voice/wake/stopword tables live
next to their features — see stage 10 in PLAN.md.
