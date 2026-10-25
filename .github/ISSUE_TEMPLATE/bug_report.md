---
name: Bug report
about: Something Mira does not do as promised
title: "[bug] "
labels: bug
---

**What happened**

**What was expected**

**Environment**
- OS: (Windows / Linux X11 / Wayland / macOS, version)
- Mira version (installer name, e.g. Mira Setup 0.1.0.exe, or source commit)
- LLM backend: (NVIDIA NIM / Ollama / LM Studio / other)
- Voice on/off, STT on/off

**Self-test**
`cd desktop && npx electron . --selftest` — exit code and which named check
failed (check names are printed in the log). A log helps: the app writes
`electron.log` next to itself.
