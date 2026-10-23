// Сборка трёх Go-бинарей для упаковки (companion, focuswatch, soundwatch).
//
// Запускается на той ОСи, где пакуют установщик (electron-builder тоже
// пакует нативно), поэтому GOOS не указываем — бинарь родной для системы.
// Вызов: node scripts/build-go.js (или npm run go из desktop/).
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const bin = path.join(root, 'bin');
fs.mkdirSync(bin, { recursive: true });

const exe = process.platform === 'win32' ? '.exe' : '';
for (const cmd of ['companion', 'focuswatch', 'soundwatch']) {
  const out = path.join(bin, cmd + exe);
  const r = spawnSync('go', ['build', '-o', out, './cmd/' + cmd], {
    cwd: path.join(root, 'server'),
    stdio: 'inherit',
  });
  if (r.status !== 0) {
    console.error('СБОРКА ' + cmd + ' НЕ УДАЛАСЬ — смотри сообщение go выше.');
    process.exit(1);
  }
  console.log('собран', out);
}
