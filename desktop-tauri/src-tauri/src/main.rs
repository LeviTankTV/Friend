// Оболочка Миры на Tauri (миграция с Electron, этап 15 из PLAN.md).
//
// Порядок тот же, что у Electron-оболочки (desktop/main.js): поднять
// Go-сервер (sidecar из resources/bin), дождаться /healthz, создать
// прозрачное окно поверх всех со страницей сервера. Страница и сервер —
// те же; меняется только оболочка.
//
// Что пока НЕ перенесено (по шагам аудита): команды моста (этап 3),
// переписка bridge.js (этап 4), трей/автозапуск/single-instance (этап 5).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

/// Адрес сервера из конфига по умолчанию (server/internal/config).
const SERVER_ADDR: &str = "127.0.0.1:8787";

/// Дочерний Go-сервер. Умирает вместе с оболочкой (Drop), как before-quit
/// у Electron-оболочки.
struct ServerChild(Mutex<Child>);

impl Drop for ServerChild {
    fn drop(&mut self) {
        if let Ok(mut child) = self.0.lock() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

/// Где лежат Go-бинари. В собранном приложении — resources/bin; при dev-прогоне
/// resources пуст, берём корень репозитория (desktop-tauri/src-tauri → вверх).
fn bin_dir(app: &tauri::AppHandle) -> Option<PathBuf> {
    if let Ok(res) = app.path().resource_dir() {
        let p = res.join("bin");
        if p.exists() {
            return Some(p);
        }
    }
    let dev = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../bin");
    if dev.exists() {
        return Some(dev);
    }
    None
}

fn wait_healthz(timeout: Duration) -> bool {
    let t0 = Instant::now();
    while t0.elapsed() < timeout {
        if std::net::TcpStream::connect(SERVER_ADDR).is_ok() {
            return true;
        }
        std::thread::sleep(Duration::from_millis(200));
    }
    false
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            // Второй экземпляр: окно уже есть у первого (аналог second-instance).
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .setup(|app| {
            let handle = app.handle().clone();
            let bin = bin_dir(&handle).ok_or("bin/ не найден: собери Go-бинари (cd server && go build -o ../bin/companion ./cmd/companion)")?;
            let server = bin.join(if cfg!(windows) { "companion.exe" } else { "companion" });
            let child = Command::new(&server).spawn().map_err(|e| {
                format!("сервер не запустился ({}): {}", server.display(), e)
            })?;
            app.manage(ServerChild(Mutex::new(child)));

            if !wait_healthz(Duration::from_secs(10)) {
                return Err("сервер не поднялся за 10 с".into());
            }

            let url: tauri::Url = format!("http://{}/", SERVER_ADDR).parse().unwrap();
            let win = WebviewWindowBuilder::new(&handle, "main", WebviewUrl::External(url))
                .title("Мира")
                .inner_size(320.0, 423.0)
                .transparent(true)
                .decorations(false)
                .resizable(false)
                .shadow(false)
                .skip_taskbar(true)
                .always_on_top(true)
                .visible(true)
                .build()?;
            let _ = win.set_focus();
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("Mira: запуск оболочки не удался");
}
