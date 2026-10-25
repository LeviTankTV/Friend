# Changelog

All notable changes to this project are documented here.
The dev diary with measurements lives in [PLAN.md](PLAN.md).

## [0.1.0] — 2026-10-25

First public release. Mira is complete across all planned stages:

- **Character on the desktop.** Transparent always-on-top window: walking,
  dragging, wheel-resize, seats (taskbar / active window / screen edges),
  peeking from the edge, sleeping after idle, tray with live face, autostart
  (Windows registry, macOS login item, Linux XDG).
- **Conversation.** Any OpenAI-compatible LLM (NVIDIA NIM, Ollama, LM Studio);
  in-character replies, chat UI, mood-aware tone reading.
- **Memory.** Local SQLite + FTS5, `remember:` facts, nightly day summaries,
  full-text search; survives restarts.
- **Mood and feelings.** Independent mood/energy/affection drift; empathy to
  the tone of your messages; accumulated sleep-deprivation pattern surfaced
  into every prompt ("take care of him" line).
- **Proactive care.** 13 initiative kinds: morning/evening rituals, late-night
  nudges, gaming tease, silence check-ins, return greeting after ≥3 h,
  follow-ups on old facts; anti-spam gap between kinds.
- **Voice.** Edge TTS output (per-sentence synthesis with pauses, night −30 %
  volume, mood/sleep prosody), local faster-whisper input, wake word per
  language.
- **Body language.** Stretching, micro-fidgets, gaze "past the screen",
  yawn after sleep lines, dancing to system audio (WASAPI/PulseAudio).
- **Cross-platform.** Windows / Linux (X11 + PulseAudio, native Go clients) /
  macOS (NSWorkspace focus, honest loopback limitation); CI matrix with live
  X11 and PulseAudio checks; 29-part Electron self-test.
- **Localization.** Full ru/en: web dictionaries, server prompt dictionaries,
  persona file, voice, wake word, STT language; `avatar.language` switch.
- **Customization.** Persona as a single editable JSON (`-dump-persona=ru|en`
  prints a template), external sprite sheets served over `/sprites/`.
- **Public kit.** Bilingual README, demo mode (`--demo`, 6-scene play with
  GIF capture), app icon from the sprite, NSIS installer for Windows.

## Planned

- More languages (i18n core is ready; languages beyond ru/en need a
  dictionary registry)
- Character gallery / sharing personalities
- More proactive care patterns (weekly rhythms, follow-ups on old facts)
- Memory browser UI (view / delete what she remembers)
