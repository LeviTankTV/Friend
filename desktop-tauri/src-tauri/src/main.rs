// Оболочка Миры на Tauri (миграция с Electron, этап 15 из PLAN.md).
//
// Порядок тот же, что у Electron-оболочки (desktop/main.js): поднять
// Go-сервер, дождаться /healthz, создать прозрачное окно поверх всех со
// страницей сервера; запустить помощников (focuswatch — активное окно
// человека, soundwatch — уровень системного звука) и пересылать их строки
// странице; держать файл настроек и сторож отрисовки.
//
// Каналы IPC (этап 3 аудита): команды invoke + события emit — полный
// аналог 17 каналов preload Electron-оболочки.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::io::{BufRead, BufReader};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::thread;
use std::time::{Duration, Instant};

use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager, State, WebviewUrl, WebviewWindowBuilder};

const SERVER_ADDR: &str = "127.0.0.1:8787";
// Сколько молчания страницы = поломка отрисовки (как BEAT_TIMEOUT у Электрона).
const BEAT_TIMEOUT_MS: u128 = 9000;
const FOCUS_INTERVAL_SECS: u64 = 4;
const SOUND_INTERVAL_MS: u64 = 200;

/// Позиция окна в ЛОГИЧЕСКИХ пикселях (DIP) — та же единица, что у страницы
/// и Electron-оболочки; физические делим на масштаб.
#[derive(Serialize, Clone, Copy)]
struct Pos {
    x: i32,
    y: i32,
}

#[derive(Serialize, Clone, Copy)]
struct Area {
    x: i32,
    y: i32,
    width: i32,
    height: i32,
}

#[derive(serde::Deserialize)]
struct Bounds {
    x: Option<i32>,
    y: Option<i32>,
    width: Option<i32>,
    height: Option<i32>,
}

/// Всё живое состояние оболочки. Дети-процессы гасим в RunEvent::Exit —
/// аналог before-quit у Electron-оболочки.
struct Shell {
    server: Mutex<Option<Child>>,
    focus: Mutex<Option<Child>>,
    sound: Mutex<Option<Child>>,
    sound_wanted: AtomicBool,
    last_focus: Mutex<Option<serde_json::Value>>,
    last_beat: Mutex<Instant>,
    settings_path: PathBuf,
    bin_dir: PathBuf,
    web_dir: PathBuf,
}

impl Shell {
    fn kill_children(&self) {
        for slot in [&self.server, &self.focus, &self.sound] {
            if let Ok(mut guard) = slot.lock() {
                if let Some(mut child) = guard.take() {
                    let _ = child.kill();
                    let _ = child.wait();
                }
            }
        }
    }
}

/// Где лежат Go-бинари и каталог страницы: в собранном приложении —
/// resources (bin/ и web/); при dev-прогоне — корень репозитория.
fn resolve_dirs(app: &AppHandle) -> (PathBuf, PathBuf) {
    if let Ok(res) = app.path().resource_dir() {
        let bin = res.join("bin");
        let web = res.join("web");
        if bin.exists() && web.exists() {
            return (bin, web);
        }
    }
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
    (root.join("bin"), root.join("web"))
}

/// Файл настроек — тот же, что у Electron-оболочки (%AppData%/Mira/settings.json):
/// выбор размера/посадки человека переживает смену оболочки.
fn settings_path(app: &AppHandle) -> PathBuf {
    if let Ok(appdata) = std::env::var("APPDATA") {
        let p = PathBuf::from(appdata).join("Mira").join("settings.json");
        if p.parent().map(|d| d.exists()).unwrap_or(false) {
            return p;
        }
    }
    app.path()
        .app_config_dir()
        .map(|d| d.join("settings.json"))
        .unwrap_or_else(|_| PathBuf::from("settings.json"))
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

// --- геометрия ------------------------------------------------------------

fn scale(win: &tauri::WebviewWindow) -> f64 {
    win.scale_factor().unwrap_or(1.0)
}

fn real_pos(win: &tauri::WebviewWindow) -> Option<Pos> {
    let p = win.outer_position().ok()?;
    let s = scale(win);
    if s <= 0.0 {
        return None;
    }
    Some(Pos {
        x: (p.x as f64 / s).round() as i32,
        y: (p.y as f64 / s).round() as i32,
    })
}

fn primary_work_area(app: &AppHandle) -> Area {
    let mon = app.primary_monitor().ok().flatten();
    if let Some(m) = mon {
        // work_area — без панели задач (та же семантика, что у Electron).
        let wa = m.work_area();
        let s = m.scale_factor();
        return Area {
            x: (wa.position.x as f64 / s).round() as i32,
            y: (wa.position.y as f64 / s).round() as i32,
            width: (wa.size.width as f64 / s).round() as i32,
            height: (wa.size.height as f64 / s).round() as i32,
        };
    }
    Area { x: 0, y: 0, width: 1280, height: 720 }
}

/// Поставить окно в логическую точку, вернув ГДЕ ОНО ОКАЗАЛОСЬ: оболочка
/// держит окно в видимой части экрана и не пускает за край — страница
/// обязана узнать настоящее место (иначе окно «возвращалось бы в никуда»).
fn set_logical_pos(win: &tauri::WebviewWindow, x: i32, y: i32) -> Result<Pos, String> {
    win.set_position(tauri::Position::Logical(tauri::LogicalPosition::new(
        x as f64, y as f64,
    )))
    .map_err(|e| e.to_string())?;
    Ok(real_pos(win).ok_or("позиция не читается")?)
}

// --- помощники ------------------------------------------------------------

fn spawn_focus(app: &AppHandle, shell: &Shell) {
    let mut guard = shell.focus.lock().unwrap();
    if guard.is_some() {
        return;
    }
    let bin = shell.bin_dir.join(if cfg!(windows) { "focuswatch.exe" } else { "focuswatch" });
    let Ok(child) = Command::new(&bin)
        .args(["-interval", &format!("{}s", FOCUS_INTERVAL_SECS)])
        // Помощник выходит, когда закрыт stdin (родитель умер). NUL-хендл даёт
        // мгновенный EOF (измерено: с /dev/null умирает за тик) — держим
        // открытую трубу: stdin живёт в Child до kill, Read блокируется.
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    else {
        eprintln!("[friend] focuswatch не запустился");
        return;
    };
    *guard = Some(child);
    let app = app.clone();
    let Some(stdout) = guard.as_mut().unwrap().stdout.take() else { return };
    thread::spawn(move || {
        let reader = BufReader::new(stdout);
        for line in reader.lines().map_while(Result::ok) {
            let Ok(data) = serde_json::from_str::<serde_json::Value>(line.trim()) else { continue };
            if data.get("process").is_some() {
                let _ = app.emit("focus:info", &data);
            }
        }
    });
}

fn stop_focus(shell: &Shell) {
    if let Ok(mut guard) = shell.focus.lock() {
        if let Some(mut child) = guard.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

fn spawn_sound(app: &AppHandle, shell: &Shell) -> bool {
    let mut guard = shell.sound.lock().unwrap();
    if guard.is_some() {
        return true;
    }
    let bin = shell.bin_dir.join(if cfg!(windows) { "soundwatch.exe" } else { "soundwatch" });
    let Ok(mut child) = Command::new(&bin)
        .args(["-interval", &format!("{}ms", SOUND_INTERVAL_MS)])
        .stdin(Stdio::piped()) // тот же stdin-сторож, что и у focuswatch
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    else {
        return false;
    };
    let app = app.clone();
    if let Some(stdout) = child.stdout.take() {
        thread::spawn(move || {
            let reader = BufReader::new(stdout);
            for line in reader.lines().map_while(Result::ok) {
                let Ok(data) = serde_json::from_str::<serde_json::Value>(line.trim()) else { continue };
                if let Some(level) = data.get("level").and_then(|v| v.as_f64()) {
                    let _ = app.emit("sound:level", level);
                }
            }
        });
    }
    *guard = Some(child);
    true
}

fn stop_sound(shell: &Shell) {
    if let Ok(mut guard) = shell.sound.lock() {
        if let Some(mut child) = guard.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

// --- команды моста (invoke) ------------------------------------------------

#[tauri::command]
fn move_by(win: tauri::WebviewWindow, dx: i32, dy: i32) -> Result<Pos, String> {
    let cur = real_pos(&win).ok_or("позиция не читается")?;
    set_logical_pos(&win, cur.x + dx, cur.y + dy)
}

#[tauri::command]
fn set_position(win: tauri::WebviewWindow, x: i32, y: i32) -> Result<Pos, String> {
    set_logical_pos(&win, x, y)
}

#[tauri::command]
fn get_position(win: tauri::WebviewWindow) -> Result<Pos, String> {
    real_pos(&win).ok_or_else(|| "позиция не читается".into())
}

#[tauri::command]
fn set_bounds(
    app: AppHandle,
    win: tauri::WebviewWindow,
    b: Bounds,
) -> Result<Pos, String> {
    let s = scale(&win);
    if let (Some(w), Some(h)) = (b.width, b.height) {
        let w = w.max(60).min(2000);
        let h = h.max(60).min(2000);
        win.set_size(tauri::Size::Logical(tauri::LogicalSize::new(
            w as f64, h as f64,
        )))
        .map_err(|e| e.to_string())?;
    }
    let cur = real_pos(&win).ok_or("позиция не читается")?;
    let x = b.x.unwrap_or(cur.x);
    let y = b.y.unwrap_or(cur.y);
    // Не пускаем окно за край экрана (та же забота, что у Electron-оболочки):
    // полностью спрятанное окно = пропавший персонаж.
    let wa = primary_work_area(&app);
    let size = win.outer_size().map(|sz| sz).unwrap_or_default();
    let win_w = (size.width as f64 / s).round() as i32;
    let _win_h = (size.height as f64 / s).round() as i32;
    let x = x.clamp(wa.x - win_w + 48, wa.x + wa.width - 48);
    let y = y.clamp(wa.y - 8, wa.y + wa.height - 48);
    set_logical_pos(&win, x, y)
}

#[tauri::command]
fn set_size(win: tauri::WebviewWindow, width: i32, height: i32) -> Result<Pos, String> {
    win.set_size(tauri::Size::Logical(tauri::LogicalSize::new(
        width as f64, height as f64,
    )))
    .map_err(|e| e.to_string())?;
    real_pos(&win).ok_or_else(|| "позиция не читается".into())
}

#[tauri::command]
fn always_on_top(win: tauri::WebviewWindow, on: bool) -> Result<(), String> {
    win.set_always_on_top(on).map_err(|e| e.to_string())
}

#[tauri::command]
fn work_area(app: AppHandle) -> Area {
    primary_work_area(&app)
}

#[tauri::command]
fn focus(win: tauri::WebviewWindow) {
    let _ = win.set_focus();
}

#[tauri::command]
fn quit(app: AppHandle) {
    app.exit(0);
}

#[tauri::command]
fn beat(shell: State<Shell>) {
    *shell.last_beat.lock().unwrap() = Instant::now();
}

#[tauri::command]
fn autostart(app: AppHandle, on: bool) -> bool {
    use tauri_plugin_autostart::ManagerExt;
    let manager = app.autolaunch();
    let _ = if on { manager.enable() } else { manager.disable() };
    manager.is_enabled().unwrap_or(false)
}

#[tauri::command]
fn dance(app: AppHandle, shell: State<Shell>, on: bool) -> bool {
    shell.sound_wanted.store(on, Ordering::Relaxed);
    if on {
        spawn_sound(&app, &shell)
    } else {
        stop_sound(&shell);
        true
    }
}

#[tauri::command]
fn save_settings(shell: State<Shell>, json: String) {
    if let Some(dir) = shell.settings_path.parent() {
        let _ = std::fs::create_dir_all(dir);
    }
    let _ = std::fs::write(&shell.settings_path, json);
}

#[tauri::command]
fn get_settings(shell: State<Shell>) -> Option<String> {
    // Свежие настройки из файла: init-скрипт отдаёт снимок на момент создания
    // окна, после перезагрузки страницы он устаревает (та же история, что у
    // аргументов preload в Electron-оболочке).
    std::fs::read_to_string(&shell.settings_path)
        .ok()
        .filter(|s| !s.trim().is_empty())
}

#[tauri::command]
fn replay_focus(app: AppHandle, shell: State<Shell>) {
    // Страница переподключилась: она просит последнюю строку помощника заново
    // (аналог send('focus:info', lastFocusData) при загрузке у Электрона).
    if let Ok(guard) = shell.last_focus.lock() {
        if let Some(data) = guard.as_ref() {
            let _ = app.emit("focus:info", data);
        }
    }
}

/// Показать/спрятать окно (трей: пункт меню и двойной клик).
fn toggle_window(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let visible = w.is_visible().unwrap_or(false);
        let _ = if visible { w.hide() } else { w.show() };
    }
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None,
        ))
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            // Второй экземпляр: показать существующее окно.
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .invoke_handler(tauri::generate_handler![
            move_by, set_position, get_position, set_bounds, set_size, always_on_top,
            work_area, focus, quit, beat, autostart, dance, save_settings, get_settings,
            replay_focus,
        ])
        .on_window_event(|window, event| {
            // Закрытие окна — прячем в трей: персонаж живёт в системе, как у
            // Electron-оболочки. Настоящий выход — пункт меню трея.
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                let _ = window.hide();
                api.prevent_close();
            }
        })
        .setup(|app| {
            let handle = app.handle().clone();
            let (bin_dir, web_dir) = resolve_dirs(&handle);
            let settings = settings_path(&handle);

            // Сервер: -web <каталог страницы> -addr 127.0.0.1:8787 (как у Электрона).
            let server_bin = bin_dir.join(if cfg!(windows) { "companion.exe" } else { "companion" });
            let child = Command::new(&server_bin)
                .args([
                    "-web",
                    web_dir.to_string_lossy().as_ref(),
                    "-addr",
                    SERVER_ADDR,
                ])
                // Логи сервера — в консоль оболочки (как у Электрона). Piped без
                // читателя забил бы буфер и заблокировал сервер на первом же
                // логе (находка прогона).
                .stdout(Stdio::inherit())
                .stderr(Stdio::inherit())
                .spawn()
                .map_err(|e| format!("сервер не запустился ({}): {}", server_bin.display(), e))?;

            // Состояние собираем здесь, где уже известны настоящие пути.
            app.manage(Shell {
                server: Mutex::new(Some(child)),
                focus: Mutex::new(None),
                sound: Mutex::new(None),
                sound_wanted: AtomicBool::new(false),
                last_focus: Mutex::new(None),
                last_beat: Mutex::new(Instant::now()),
                settings_path: settings.clone(),
                bin_dir,
                web_dir,
            });

            if !wait_healthz(Duration::from_secs(10)) {
                return Err("сервер не поднялся за 10 с".into());
            }

            // Настройки — в страницу ДО её скриптов: меню читает их при сборке.
            // Адрес сервера — тоже: страница живёт в ассет-протоколе оболочки
            // (tauri://localhost), а сервер — на 127.0.0.1:8787; мост собирает
            // из этого абсолютные URL (ws, /stt, /memory, /tts, /sprites).
            let settings_json = std::fs::read_to_string(&settings).unwrap_or_else(|_| "null".into());
            let init_script = format!(
                "window.__FRIEND_SERVER__ = 'http://{}'; window.__FRIEND_SETTINGS__ = {};",
                SERVER_ADDR,
                if settings_json.trim().is_empty() { "null".into() } else { settings_json }
            );

            // Страница — из ассетов оболочки (build.frontendDist → ../web):
            // Tauri v2 не даёт удалённым страницам звать команды приложения
            // (ACL), а ассет-страница — своя, «local: true» (находка прогона:
            // remote-страница получала «get_position not allowed. Plugin not
            // found»). Разница origin компенсируется CORS на сервере.
            let win = WebviewWindowBuilder::new(&handle, "main", WebviewUrl::App("index.html".into()))
                .title("Мира")
                .inner_size(320.0, 423.0)
                .transparent(true)
                .decorations(false)
                .resizable(false)
                .shadow(false)
                .skip_taskbar(true)
                .always_on_top(true)
                .initialization_script(&init_script)
                .build()?;
            let _ = win.set_focus();

            // Трей: «показать / скрыть» и «выход», двойной клик — показать.
            // Иконка пока статичная (спрайт-лицо); живая иконка из capturePage —
            // шаг 5+ аудита (в Tauri нужен COM CapturePreview).
            let show_i = tauri::menu::MenuItem::with_id(app, "show", "показать / скрыть", true, None::<&str>)?;
            let quit_i = tauri::menu::MenuItem::with_id(app, "quit", "выход", true, None::<&str>)?;
            let tray_menu = tauri::menu::Menu::with_items(app, &[&show_i, &quit_i])?;
            let icon = tauri::image::Image::from_bytes(include_bytes!("../icons/icon.png"))?;
            let _tray = tauri::tray::TrayIconBuilder::with_id("main")
                .icon(icon)
                .tooltip("Мира — двойной клик: показать")
                .menu(&tray_menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, ev| match ev.id().as_ref() {
                    "show" => toggle_window(app),
                    "quit" => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, ev| {
                    if let tauri::tray::TrayIconEvent::DoubleClick { .. } = ev {
                        toggle_window(tray.app_handle());
                    }
                })
                .build(app)?;

            // Помощник активного окна: строки — события focus:info странице.
            {
                let shell: State<Shell> = app.state();
                spawn_focus(&handle, &shell);
            }

            // Сторож отрисовки: страница молчит дольше BEAT_TIMEOUT —
            // перезагружаем её (аналог checkDrawing у Электрона).
            {
                let w = win.clone();
                let app_h = handle.clone();
                thread::spawn(move || loop {
                    thread::sleep(Duration::from_secs(2));
                    let Some(shell) = app_h.try_state::<Shell>() else { continue };
                    let quiet = {
                        let last = shell.last_beat.lock().unwrap();
                        last.elapsed().as_millis()
                    };
                    if quiet > BEAT_TIMEOUT_MS {
                        *shell.last_beat.lock().unwrap() = Instant::now();
                        let _ = w.eval("location.reload()");
                    }
                });
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("Mira: запуск оболочки не удался")
        .run(|app, event| {
            if let tauri::RunEvent::Exit = event {
                if let Some(shell) = app.try_state::<Shell>() {
                    shell.kill_children();
                }
            }
        });
}
