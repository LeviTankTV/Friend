package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write кладёт файл во временный каталог и возвращает путь.
func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Главный сценарий: человек вставил ключ в файл — сервер его увидел.
func TestLoadAppliesKey(t *testing.T) {
	t.Setenv("FRIEND_TEST_KEY", "")
	_ = os.Unsetenv("FRIEND_TEST_KEY")
	p := write(t, ".env", "# ключ NVIDIA\nNVIDIA_API_KEY=nvapi-12345\n")

	res, err := Load(p)
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
	if res.Path != p {
		t.Errorf("прочитанный файл %q, ожидался %q", res.Path, p)
	}
	if got := os.Getenv("NVIDIA_API_KEY"); got != "nvapi-12345" {
		t.Errorf("ключ в окружении %q, ожидался nvapi-12345", got)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "NVIDIA_API_KEY" {
		t.Errorf("Applied = %v, ожидался [NVIDIA_API_KEY]", res.Applied)
	}
}

// Настоящая переменная окружения главнее файла: иначе «я же поменял ключ»
// превращается в поиск причины, почему сервер взял старое значение.
func TestExistingEnvWins(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "из-среды")
	p := write(t, ".env", "NVIDIA_API_KEY=из-файла\n")

	res, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("NVIDIA_API_KEY"); got != "из-среды" {
		t.Errorf("значение %q, ожидалось «из-среды» (файл не должен перекрывать среду)", got)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "NVIDIA_API_KEY" {
		t.Errorf("Skipped = %v, ожидался [NVIDIA_API_KEY]", res.Skipped)
	}
}

// Мелочи формата, которые ломают чтение у людей: кавычки, пробелы, export,
// комментарии на строке с ключом, пустое значение.
func TestFormatTolerance(t *testing.T) {
	p := write(t, ".env", strings.Join([]string{
		"# комментарий",
		"",
		"   ",
		"export EXPORTED_KEY=значение",
		`QUOTED_KEY="в кавычках"`,
		"SINGLE_KEY='одинарные'",
		"SPACED_KEY =   пробелы вокруг  ",
		"EMPTY_KEY=",
		"мусор без знака равно",
		"ПЛОХОЕ_ИМЯ=значение",
		"NVIDIA_API_KEY=nvapi-9",
	}, "\n"))

	wantKeys := []string{"EXPORTED_KEY", "QUOTED_KEY", "SINGLE_KEY", "SPACED_KEY", "NVIDIA_API_KEY"}
	for _, name := range append([]string{"EMPTY_KEY"}, wantKeys...) {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}

	res, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != len(wantKeys) {
		t.Errorf("применилось %v, ожидались ровно %v", res.Applied, wantKeys)
	}
	want := map[string]string{
		"EXPORTED_KEY":   "значение",
		"QUOTED_KEY":     "в кавычках",
		"SINGLE_KEY":     "одинарные",
		"SPACED_KEY":     "пробелы вокруг",
		"NVIDIA_API_KEY": "nvapi-9",
	}
	for name, wantVal := range want {
		if got := os.Getenv(name); got != wantVal {
			t.Errorf("%s = %q, ожидалось %q", name, got, wantVal)
		}
	}
	// Пустая переменная из файла не должна попадать в окружение: она перебила
	// бы настоящую (и тогда «ключа нет» выглядело бы как «ключ пустой»).
	if _, exists := os.LookupEnv("EMPTY_KEY"); exists {
		t.Error("EMPTY_KEY попала в окружение пустой — этого быть не должно")
	}
	if _, exists := os.LookupEnv("ПЛОХОЕ_ИМЯ"); exists {
		t.Error("имя переменной с недопустимыми символами попало в окружение")
	}
}

// Отсутствие файлов — не ошибка: компаньон работает и без модели.
func TestMissingFilesAreFine(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".env")
	res, err := Load(missing)
	if err != nil {
		t.Fatalf("отсутствие файла дало ошибку: %v", err)
	}
	if res.Found() {
		t.Error("Found() = true, хотя файла нет")
	}
}

// Берётся ПЕРВЫЙ существующий файл из списка, а не все подряд.
func TestFirstExistingWins(t *testing.T) {
	dir := t.TempDir()
	second := filepath.Join(dir, "second.env")
	first := filepath.Join(dir, "first.env")
	if err := os.WriteFile(second, []byte("ORDER_KEY=второй\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("ORDER_KEY=первый\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORDER_KEY", "")
	_ = os.Unsetenv("ORDER_KEY")

	res, err := Load(filepath.Join(dir, "нет.env"), first, second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != first {
		t.Errorf("прочитан %q, ожидался первый существующий %q", res.Path, first)
	}
	if got := os.Getenv("ORDER_KEY"); got != "первый" {
		t.Errorf("ORDER_KEY = %q, ожидалось «первый»", got)
	}
}

// FRIEND_ENV_FILE в списке кандидатов стоит первым: так можно указать свой файл
// ключей, не трогая остальное.
func TestCandidatesPrefersExplicitFile(t *testing.T) {
	t.Setenv("FRIEND_ENV_FILE", "D:/keys/my.env")
	got := Candidates("C:/project", "C:/data")
	if len(got) == 0 || got[0] != "D:/keys/my.env" {
		t.Fatalf("первый кандидат %v, ожидался явный файл", got)
	}
	if len(got) != 3 {
		t.Errorf("кандидатов %d, ожидалось 3: %v", len(got), got)
	}
}
