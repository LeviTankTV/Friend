// Package dotenv читает файл .env и кладёт значения в окружение процесса.
//
// Зачем отдельный файл, если ключ и так берётся из окружения: «задать переменную
// окружения» на Windows — это либо setx с перезапуском терминала, либо правка
// реестра. Человек, который просто хочет вставить ключ NVIDIA, не должен этого
// делать. Поэтому сервер сам читает готовый файл: открыл .env в блокноте,
// вставил ключ, запустил start.bat.
//
// Правила, которые здесь важны:
//   - уже заданная переменная окружения ПОБЕЖДАЕТ файл. Настоящая среда главнее
//     удобного файла, иначе отладка «я же поменял ключ» превращается в поиск
//     причины, почему сервер взял старое значение;
//   - значения НИКОГДА не возвращаются наружу и не логируются: наружу отдаются
//     только имя файла и имена переменных;
//   - файла может не быть — это обычная ситуация (компаньон работает без
//     модели), а не ошибка.
package dotenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxFileBytes — предел чтения. Файл с ключами — это десяток строк; больше
// читать нечего, а память тратится на каждом запуске.
const maxFileBytes = 64 << 10

// Result — что получилось из чтения файла.
type Result struct {
	// Path — прочитанный файл ("" — ни одного из перечисленных не нашлось).
	Path string
	// Applied — имена переменных, которые применились из файла.
	Applied []string
	// Skipped — имена, которые уже были заданы в окружении: файл их не тронул.
	Skipped []string
}

// Found — был ли прочитан хоть один файл.
func (r Result) Found() bool { return r.Path != "" }

// Load читает ПЕРВЫЙ существующий файл из paths и применяет его значения.
//
// Отсутствие всех файлов — не ошибка (Result без Path). Ошибкой считается
// только файл, который есть, но не читается: это уже «права/диск», о чём надо
// сказать.
func Load(paths ...string) (Result, error) {
	var res Result
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return res, fmt.Errorf("файл ключей %s: %w", p, err)
		}
		if len(raw) > maxFileBytes {
			return res, fmt.Errorf("файл ключей %s больше %d КБ — это не файл с ключами", p, maxFileBytes>>10)
		}
		res.Path = p
		apply(&res, string(raw))
		return res, nil
	}
	return res, nil
}

// apply разбирает содержимое файла.
func apply(res *Result, text string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// «export KEY=VALUE» — привычка из unix-миров; принимаем и её, чтобы
		// файл, скопированный из чужого проекта, работал.
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue // строка без «=» — не пара ключ-значение, молча пропускаем
		}
		name = strings.TrimSpace(name)
		if name == "" || !validName(name) {
			continue
		}
		value = unquote(strings.TrimSpace(value))
		if value == "" {
			// Пустое значение — это «ещё не вписал ключ». Ставить пустую
			// переменную в окружение нельзя: она перебила бы настоящую.
			res.Skipped = append(res.Skipped, name)
			continue
		}
		if _, exists := os.LookupEnv(name); exists && strings.TrimSpace(os.Getenv(name)) != "" {
			res.Skipped = append(res.Skipped, name)
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			res.Skipped = append(res.Skipped, name)
			continue
		}
		res.Applied = append(res.Applied, name)
	}
}

// unquote снимает обрамляющие кавычки: люди пишут и KEY="значение", и KEY=значение.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// validName проверяет, что это имя переменной окружения, а не строка текста:
// иначе случайная строка из файла стала бы переменной с мусорным именем.
func validName(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Candidates — где искать файл ключей по умолчанию, в порядке приоритета:
// рядом с проектом (там его видно и легко править), затем в каталоге данных
// компаньона (там живёт конфиг и состояние).
func Candidates(projectDir, dataDir string) []string {
	var out []string
	if p := strings.TrimSpace(os.Getenv("FRIEND_ENV_FILE")); p != "" {
		out = append(out, p)
	}
	if projectDir != "" {
		out = append(out, filepath.Join(projectDir, ".env"))
	}
	if dataDir != "" {
		out = append(out, filepath.Join(dataDir, ".env"))
	}
	return out
}
