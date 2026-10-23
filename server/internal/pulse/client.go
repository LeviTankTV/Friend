package pulse

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Conn — соединение с сервером PulseAudio (или pipewire-pulse): авторизация
// по cookie и обмен управляющими пакетами.
type Conn struct {
	conn    net.Conn
	version uint32
	nextTag uint32
}

// Connect находит сокет сервера, проходит AUTH и проверяет версию протокола
// (ниже 22 раскладка create_record_stream другая — честно отказываемся).
func Connect() (*Conn, error) {
	path, err := socketPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("pulse: сокет %s: %w", path, err)
	}
	c := &Conn{conn: conn, nextTag: 1}
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))

	t := c.tag()
	if err := writeControl(conn, buildAuth(loadCookie(), t)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("pulse: %w", err)
	}
	r, err := c.waitReply(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	// В ответе AUTH сервер называет свою версию (возможно с битами SHM/memfd
	// в старших разрядах — срезаем). Поля нет только у допотопных серверов.
	if v, e := r.u32(); e == nil {
		c.version = v & 0x3FFFFFFF
	}
	if c.version != 0 && c.version < 22 {
		conn.Close()
		return nil, fmt.Errorf("pulse: версия протокола сервера %d — нужна ≥22", c.version)
	}
	return c, nil
}

// Close закрывает соединение.
func (c *Conn) Close() error { return c.conn.Close() }

// tag выдаёт номер следующего запроса (внутри соединения уникален).
func (c *Conn) tag() uint32 {
	t := c.nextTag
	c.nextTag++
	return t
}

// DefaultMonitor — имя источника-монитора дефолтного sink: то, что играет
// на колонках. GET_SERVER_INFO отдаёт имя sink, монитор зовётся
// «<имя>.monitor» — отдельного поля для него в протоколе нет.
func (c *Conn) DefaultMonitor() (string, error) {
	t := c.tag()
	if err := writeControl(c.conn, buildServerInfo(t)); err != nil {
		return "", fmt.Errorf("pulse: %w", err)
	}
	r, err := c.waitReply(t)
	if err != nil {
		return "", err
	}
	// Ответ: user_name, host_name, server_version, server_name — четыре
	// строки, sample_spec (пропуск), затем default_sink_name. Остальное
	// (default_source, cookie, channel_map) не читаем — позиционно мы уже
	// стоим на нужном поле.
	for i := 0; i < 4; i++ {
		if _, err := r.str(); err != nil {
			return "", err
		}
	}
	if err := r.skipSampleSpec(); err != nil {
		return "", err
	}
	sink, err := r.str()
	if err != nil {
		return "", err
	}
	if sink == "" {
		return "", fmt.Errorf("pulse: сервер не назвал дефолтный sink")
	}
	return sink + ".monitor", nil
}

// Stream — открытый записывающий поток с монитора: мемблоки читаются по
// одному кадру, внутри PCM s16le mono 8 кГц.
type Stream struct {
	conn    *Conn
	channel uint32
}

// Record открывает записывающий поток на источнике (например, мониторе).
func (c *Conn) Record(source string) (*Stream, error) {
	t := c.tag()
	if err := writeControl(c.conn, buildRecordStream(source, t)); err != nil {
		return nil, fmt.Errorf("pulse: %w", err)
	}
	r, err := c.waitReply(t)
	if err != nil {
		return nil, err
	}
	// Первый поле ответа — канал мемблоков (остальное не читаем).
	ch, err := r.u32()
	if err != nil {
		return nil, err
	}
	return &Stream{conn: c, channel: ch}, nil
}

// Start снимает дедлайн рукопожатия: дальше поток живёт без таймаутов,
// смерть сокета ловится чтением.
func (s *Stream) Start() { s.conn.conn.SetDeadline(time.Time{}) }

// Next читает следующий мемблок с PCM-байтами. Управляющие кадры по пути
// разбираются: ошибка и убийство потока — ошибка наружу (оболочка
// переподключится), подвеска/перемещения/старты — не беда, пропускаем.
func (s *Stream) Next() ([]byte, error) {
	for {
		f, err := readFrame(s.conn.conn)
		if err != nil {
			return nil, fmt.Errorf("pulse: %w", err)
		}
		if f.channel == s.channel {
			return f.payload, nil
		}
		if f.channel != ctrlChannel {
			continue // чужой мемблок — не бывает, но хуже от него не станет
		}
		r := &reader{buf: f.payload}
		cmd, err := r.u32()
		if err != nil {
			continue // битый управляющий кадр — не повод рвать поток
		}
		if _, err := r.u32(); err != nil {
			continue // тег
		}
		switch cmd {
		case cmdError:
			code, _ := r.u32()
			return nil, fmt.Errorf("pulse: сервер отклонил (%s)", errText(code))
		case cmdRecordStreamKilled:
			return nil, fmt.Errorf("pulse: поток убит сервером")
		}
	}
}

// waitReply отправляет пакет и ждёт ответ с тем же тегом (управляющие
// события мимо тега — пропускаем).
func (c *Conn) waitReply(tag uint32) (*reader, error) {
	for {
		f, err := readFrame(c.conn)
		if err != nil {
			return nil, fmt.Errorf("pulse: %w", err)
		}
		if f.channel != ctrlChannel {
			continue // мемблок до ответа — не бывает, пропускаем
		}
		r := &reader{buf: f.payload}
		cmd, err := r.u32()
		if err != nil {
			return nil, err
		}
		t, err := r.u32()
		if err != nil {
			return nil, err
		}
		if cmd == cmdError {
			code, _ := r.u32()
			return nil, fmt.Errorf("pulse: сервер отклонил (%s)", errText(code))
		}
		if t == tag {
			return r, nil
		}
	}
}

// errText — известные коды ошибок из def.h (pa_error_code); неизвестные
// показываем числом — всё лучше пустоты.
func errText(code uint32) string {
	switch code {
	case 1:
		return "доступ запрещён"
	case 2:
		return "неизвестная команда"
	case 3:
		return "неверный аргумент"
	case 7:
		return "ошибка протокола"
	case 8:
		return "таймаут"
	case 9:
		return "cookie не принят"
	case 17:
		return "версия протокола не подходит"
	case 11:
		return "соединение закрыто"
	}
	return fmt.Sprintf("код %d", code)
}

// socketPath — где искать сокет сервера: $PULSE_SERVER (unix:…), затем
// $XDG_RUNTIME_DIR/pulse/native, затем /run/user/<uid>/pulse/native
// (PipeWire слушает там же через pipewire-pulse).
func socketPath() (string, error) {
	if v := os.Getenv("PULSE_SERVER"); strings.HasPrefix(v, "unix:") {
		return strings.TrimPrefix(v, "unix:"), nil
	}
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "pulse", "native"), nil
	}
	if os.Getuid() > 0 {
		return fmt.Sprintf("/run/user/%d/pulse/native", os.Getuid()), nil
	}
	return "", fmt.Errorf("pulse: не найден каталог времени выполнения (XDG_RUNTIME_DIR пуст)")
}

// loadCookie — cookie авторизации: $PULSE_COOKIE, затем конфиг пользователя.
// Файла нет — нули: сервер сравнит и откажет честной ошибкой (или пустит по
// uid сокета — зависит от настроек сервера).
func loadCookie() []byte {
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" && home != "" {
		cfg = filepath.Join(home, ".config")
	}
	paths := []string{}
	if p := os.Getenv("PULSE_COOKIE"); p != "" {
		paths = append(paths, p)
	}
	if cfg != "" {
		paths = append(paths, filepath.Join(cfg, "pulse", "cookie"))
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".pulse-cookie"))
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}
