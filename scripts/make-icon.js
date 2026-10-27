// make-icon.js — иконка приложения из самой Миры.
//
// Запуск: cd desktop && npx electron ../scripts/make-icon.js
// Поднимает сервер (как оболочка), грузит страницу в НЕВИДИМОМ окне,
// ставит максимальный масштаб спрайта, снимает кадр с прозрачностью,
// обрезает по альфа-каналу до квадрата 256×256 и пишет build/icon.ico
// (ICO с PNG внутри — штатный формат со времён Vista).
const { app, BrowserWindow } = require('electron');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');

const ADDR = '127.0.0.1:8796';
let serverProc = null;

function ping() {
  return new Promise((resolve) => {
    const req = http.get({ host: '127.0.0.1', port: 8796, path: '/healthz', timeout: 500 },
      (res) => { res.resume(); resolve(res.statusCode === 200); });
    req.on('error', () => resolve(false));
    req.on('timeout', () => { req.destroy(); resolve(false); });
  });
}

// icoFromPng — ICO-контейнер с одним PNG-изображением.
function icoFromPng(png) {
  const head = Buffer.alloc(6 + 16);
  head.writeUInt16LE(0, 0);  // зарезервировано
  head.writeUInt16LE(1, 2);  // тип: иконка
  head.writeUInt16LE(1, 4);  // одно изображение
  head[6] = 0;               // ширина 256 = 0
  head[7] = 0;               // высота 256 = 0
  head[8] = 0;               // палитры нет
  head[9] = 0;               // зарезервировано
  head.writeUInt16LE(1, 10); // плоскости
  head.writeUInt16LE(32, 12);
  head.writeUInt32LE(png.length, 14);
  head.writeUInt32LE(22, 18); // смещение данных
  return Buffer.concat([head, png]);
}

app.whenReady().then(async () => {
  if (!(await ping())) {
    const exe = path.join(__dirname, '..', 'bin', process.platform === 'win32' ? 'companion.exe' : 'companion');
    // Проактив глушится временным конфигом (как в самопроверке): иначе ночью
    // ритуал заговорит в кадр — замерено живым прогоном.
    const stCfg = path.join(require('node:os').tmpdir(), 'friend-icon-config.json');
    fs.writeFileSync(stCfg, JSON.stringify({ proactive: { enabled: false } }));
    serverProc = spawn(exe, ['-web', path.join(__dirname, '..', 'web'), '-addr', ADDR, '-config', stCfg],
      { cwd: path.join(__dirname, '..'), stdio: 'ignore' });
    for (let i = 0; i < 40 && !(await ping()); i++) {
      await new Promise((r) => setTimeout(r, 250));
    }
  }
  // show:true, но за пределами экрана: у скрытого окна (show:false)
  // композитор не перерисовывает кадр, и capturePage отдаёт УСТАРЕВШИЙ
  // первый снимок — с облачком из истории (замерено: DOM скрыт, кадр нет).
  const win = new BrowserWindow({
    show: true, x: -3000, y: 0, transparent: true, frame: false,
    width: 700, height: 900,
    webPreferences: { backgroundThrottling: false },
  });
  await win.loadURL('http://' + ADDR + '/');
  await new Promise((r) => setTimeout(r, 2500)); // welcome, позиция, спрайт

  // Максимальный масштаб: у спрайта 40×56 клеток, 12 px/клетка = 480×672.
  await win.webContents.executeJavaScript(
    'window.friendApp.menu.setPix ? window.friendApp.menu.setPix(12) : window.friendApp.applySize(12)');
  await new Promise((r) => setTimeout(r, 1200));
  // Чистый кадр: облачко истории, HUD и индикаторы — со сцены.
  await win.webContents.executeJavaScript(`(() => {
    window.friendApp.chat.hideBubble();
    for (const id of ['hud', 'dot', 'name', 'hint']) {
      const el = document.getElementById(id);
      if (el) el.style.display = 'none';
    }
    document.querySelectorAll('.wake-ind, .menu, .chat-input').forEach((el) => el.remove());
  })()`);
  await new Promise((r) => setTimeout(r, 400));
  // Последняя зачистка прямо перед снимком: поздняя реплика не должна успеть.
  const diag = await win.webContents.executeJavaScript(`(() => {
    const fa = window.friendApp;
    if (!fa) return 'нет friendApp';
    fa.chat.hideBubble();
    const hud = document.getElementById('hud');
    if (hud) hud.style.display = 'none';
    const b = document.querySelector('.bubble');
    return 'bubble hidden=' + (b ? b.hidden : 'нет') + ' hud=' + (hud ? hud.style.display : 'нет');
  })()`);
  console.log('make-icon: зачистка —', diag);

  const img = await win.webContents.capturePage();
  const { width, height } = img.getSize();
  const buf = img.toBitmap();

  // Границы спрайта по альфа-каналу (BGRA, шаг 4).
  let x0 = width, y0 = height, x1 = 0, y1 = 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      if (buf[(y * width + x) * 4 + 3] > 10) {
        if (x < x0) x0 = x;
        if (x > x1) x1 = x;
        if (y < y0) y0 = y;
        if (y > y1) y1 = y;
      }
    }
  }
  if (x1 <= x0 || y1 <= y0) {
    console.error('make-icon: спрайт не найден (окно пустое)');
    app.exit(1);
    return;
  }
  // Квадрат по большей стороне, центрированный. PNG — 512×512: mac dmg у
  // electron-builder требует иконку минимум 512 (находка CI). ICO — 256
  // (Windows-формат со времён Vista; icoFromPng ждёт кадр ≤256).
  const side = Math.max(x1 - x0, y1 - y0);
  const cx = (x0 + x1) / 2, cy = (y0 + y1) / 2;
  const sx = Math.max(0, Math.round(cx - side / 2));
  const sy = Math.max(0, Math.round(cy - side / 2));
  const sw = Math.min(side, width - sx), sh = Math.min(side, height - sy);
  const cropped = img.crop({ x: sx, y: sy, width: sw, height: sh });
  const final512 = cropped.resize({ width: 512, height: 512 });
  const final256 = cropped.resize({ width: 256, height: 256 });
  const outDir = path.join(__dirname, '..', 'desktop', 'build');
  fs.mkdirSync(outDir, { recursive: true });
  const out = path.join(outDir, 'icon.ico');
  fs.writeFileSync(out, icoFromPng(final256.toPNG()));
  // PNG для mac dmg и Tauri (минимум 512).
  fs.writeFileSync(path.join(outDir, 'icon.png'), final512.toPNG());
  console.log('make-icon: готово —', out, '(' + fs.statSync(out).size + ' байт)');
  if (serverProc) serverProc.kill();
  app.exit(0);
});
