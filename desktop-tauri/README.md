# desktop-tauri — оболочка Mira на Tauri v2

Экспериментальная замена Electron-оболочки (`desktop/`). Страница и
Go-сервер — те же (`web/`, `bin/companion.exe`); меняется только оболочка.
Аудит и план — в PLAN.md, «Этап 15».

## Запуск в dev-режиме

```bash
cd server && go build -o ../bin/companion ./cmd/companion   # бинари для resources
cd desktop-tauri && npm install
npm run dev        # tauri dev: окно поверх всех со страницей 127.0.0.1:8787
```

## Сборка установщика

```bash
npm run build      # tauri build → src-tauri/target/release/bundle/
```

## Локальная специфика тулчейна (эта машина)

Активный тулчейн — `stable-x86_64-pc-windows-gnu`, а в PATH лежит
TDM-GCC 10.3, у которого нет `libgcc_eh.a` (rust линкует его всегда).
Рабочий обход, найденный 2026-10-26:

```bash
export RUSTFLAGS="-C link-self-contained=no -C link-arg=-LC:/Users/Tima/.rustup/tdm-fix"
# ~/.rustup/tdm-fix/libgcc_eh.a — копия C:/TDM-GCC-64/.../libgcc.a
```

`link-self-contained=yes` (родной MinGW rustup) не годится: его dlltool
падает с CreateProcess. На CI (GitHub Actions) runner'ы имеют MSVC —
флаг не нужен, сборка идёт дефолтным stable-msvc.
