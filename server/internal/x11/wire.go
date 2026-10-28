// Package x11 — клиент X11 на чистом Go: никаких cgo, никаких внешних
// модулей. Нужен ровно три вещи: какое окно активно (_NET_ACTIVE_WINDOW),
// его имя (_NET_WM_NAME), pid процесса (_NET_WM_PID), геометрия (GetGeometry)
// и простой в секундах (MIT-SCREEN-SAVER QueryInfo).
//
// Приватность та же, что на Windows: наружу уходят ИМЯ ПРОЦЕССА и ЗАГОЛОВОК
// окна (плюс прямоугольник для посадки) — никакого содержимого окон здесь
// физически и быть не может: протокол умеет только свойства окна.
package x11

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// trace — пошаговая диагностика рукопожатия в stderr. На CI-xvfb помощник
// зависал молча (ни строки вывода за 120 секунд), и без шагов с таймингом
// было невозможно сказать, НА КАКОМ шаге обрывается handshake: dial,
// запись запроса, первый байт ответа или хвост setup-ответа.
var traceT0 = time.Now()

func trace(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "x11 %5dms: "+format+"\n",
		append([]any{time.Since(traceT0).Milliseconds()}, args...)...)
}

// Display — разбор переменной DISPLAY. Понимаем локальный X-сервер
// («:0», «:10.0» — unix-сокет /tmp/.X11-unix/XN) и localhost по TCP
// («localhost:0»). X-сервер на чужой машине нам не нужен: помощник живёт
// рядом с экраном, который смотрит человек.
type Display struct {
	Socket string // путь unix-сокета (для «:0» — /tmp/.X11-unix/X0)
	TCP    string // «host:port» (для «localhost:0») — пусто, если сокет
	Screen int    // номер экрана из «:0.1» (обычно 0)
	Number int    // номер дисплея N
}

// ParseDisplay разбирает значение DISPLAY. Ошибки — с человеческой причиной.
func ParseDisplay(s string) (Display, error) {
	if s == "" {
		return Display{}, errors.New("DISPLAY не задан")
	}
	host, rest, found := strings.Cut(s, ":")
	if !found {
		return Display{}, fmt.Errorf("DISPLAY %q: нет номера дисплея", s)
	}
	if host != "" && host != "localhost" {
		return Display{}, fmt.Errorf("DISPLAY %q: помощник умеет только локальный X-сервер", s)
	}
	numStr := rest
	if i := strings.IndexAny(rest, "."); i >= 0 {
		numStr = rest[:i]
	}
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return Display{}, fmt.Errorf("DISPLAY %q: номер дисплея не число", s)
	}
	screen := 0
	if len(rest) > len(numStr)+1 {
		screen, _ = strconv.Atoi(rest[len(numStr)+1:])
	}
	d := Display{Screen: screen, Number: num}
	if host == "localhost" {
		d.TCP = fmt.Sprintf("localhost:%d", 6000+num)
	} else {
		d.Socket = fmt.Sprintf("/tmp/.X11-unix/X%d", num)
	}
	return d, nil
}

// Cookie — запись файла Xauthority (MIT-MAGIC-COOKIE-1): имя метода и данные.
type Cookie struct {
	Name string
	Data []byte
}

// family-коды Xauthority (X.h). FamilyLocal (256) — «локальный пир»,
// аутентификация по пир-контентам сокета, cookie не нужен.
const (
	familyWildMask          = 0
	familyLocal             = 256
	familyNET               = 19
	familyServerInterpreted = 11
)

// ParseXauthority читает файл Xauthority и отдаёт cookie для указанного
// дисплея. Формат (struct xauth из Xau): family(u16), addr(строка),
// number(строка), name(строка), data(строка); ВСЕ целые поля — БОЛЬШИМ
// байтом (big-endian), как пишет xauth. Находка CI-xvfb по hex-зонду
// (реальные байты: 0100 000d 7275… 0002 3939 0012 MIT-MAGIC-COOKIE-1 0010
// 16-байтный cookie): чтение длин little-endian не находило cookie,
// помощник уходил в рукопожатие без аутентификации, а Xvfb на
// неаутентифицированный запрос молчит до LoginTimeout — отсюда
// «молчаливый вис» прошлых прогонов.
func ParseXauthority(path string, display Display) (Cookie, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Cookie{}, err
	}
	p := raw
	rd := func() (string, error) {
		if len(p) < 2 {
			return "", errors.New("Xauthority: обрыв")
		}
		n := int(binary.BigEndian.Uint16(p))
		p = p[2:]
		if len(p) < n {
			return "", errors.New("Xauthority: обрыв")
		}
		s := string(p[:n])
		p = p[n:]
		return s, nil
	}
	wantNum := strconv.Itoa(display.Number)
	for len(p) > 0 {
		if len(p) < 2 {
			return Cookie{}, errors.New("Xauthority: обрыв")
		}
		fam := int(binary.BigEndian.Uint16(p))
		p = p[2:]
		// Адрес (имя хоста) в записи нас не интересует: локальные семейства
		// и так годятся, чужие адреса мы не принимаем.
		if _, err := rd(); err != nil {
			return Cookie{}, err
		}
		num, err := rd()
		if err != nil {
			return Cookie{}, err
		}
		name, err := rd()
		if err != nil {
			return Cookie{}, err
		}
		data, err := rd()
		if err != nil {
			return Cookie{}, err
		}
		// FamilyLocal: адрес устаревший и почти всегда пустой, номер
		// дисплея обязано совпадать. Остальные семейства сопоставляем по
		// адресу с локальным хостом.
		numOk := num == wantNum
		famOk := fam == familyLocal || fam == familyWildMask ||
			fam == familyServerInterpreted || fam == familyNET
		if numOk && famOk && name == "MIT-MAGIC-COOKIE-1" {
			return Cookie{Name: name, Data: []byte(data)}, nil
		}
	}
	return Cookie{}, errors.New("Xauthority: cookie для этого дисплея не найден")
}

// xauthScan — запасной поиск cookie по байтам файла: находим строку
// «MIT-MAGIC-COOKIE-1», проверяем, что перед ней длина строки (у16 ИЛИ у32 —
// ровно ей равная), а за ней — данные с длиной той же ширины, и что номер
// дисплея в записи совпадает. Формат netstrings в Xauthority на разных
// сборках xauth разнится (находка CI-xvfb: и у16-парсер дал «обрыв»), а
// судьбу помощника рядом с экраном эти тонкости решать не должны.
func xauthScan(raw []byte, display Display) (Cookie, bool) {
	needle := []byte("MIT-MAGIC-COOKIE-1")
	want := strconv.Itoa(display.Number)
	for off := 0; off+len(needle) <= len(raw); {
		j := bytes.Index(raw[off:], needle)
		if j < 0 {
			break
		}
		k := off + j // начало строки имени
		// Ширину префикса проверяем по 4 байтам ПЕРВОЙ: у u16-файла
		// «00 12» с двумя байтами номера в u32-чтении не равно 18, а у
		// u32-файла обе проверки верны — u16-выбор сдвинул бы данные на 2.
		width := 0
		if k >= 4 && binary.BigEndian.Uint32(raw[k-4:]) == uint32(len(needle)) {
			width = 4
		} else if k >= 2 && binary.BigEndian.Uint16(raw[k-2:]) == uint16(len(needle)) {
			width = 2
		}
		if width == 0 {
			off = k + 1
			continue
		}
		// Номер дисплея: строка перед именем (свой префикс длины той же ширины).
		seg := raw[:k-width] // до префикса длины имени
		if len(seg) < 2*width+len(want) || string(seg[len(seg)-len(want):]) != want {
			off = k + 1
			continue
		}
		p := len(seg) - len(want)
		if p < width {
			off = k + 1
			continue
		}
		// Префикс длины номера — ПЕРЕД строкой номера.
		n := int(binary.BigEndian.Uint16(raw[p-width:]))
		if width == 4 {
			n = int(binary.BigEndian.Uint32(raw[p-width:]))
		}
		if n != len(want) {
			off = k + 1
			continue
		}
		// Данные сразу за именем: префикс длины той же ширины.
		dl := k + len(needle)
		dlen := 0
		if dl+width <= len(raw) {
			dlen = int(binary.BigEndian.Uint16(raw[dl:]))
			if width == 4 {
				dlen = int(binary.BigEndian.Uint32(raw[dl:]))
			}
		}
		if dlen <= 0 || dlen > 128 || dl+width+dlen > len(raw) {
			off = k + 1
			continue
		}
		data := append([]byte(nil), raw[dl+width:dl+width+dlen]...)
		return Cookie{Name: string(needle), Data: data}, true
	}
	return Cookie{}, false
}

// Connect открывает соединение с локальным X-сервером по DISPLAY и
// проходит рукопожатие (с cookie из XAUTHORITY, если он есть).
func Connect() (*Client, error) {
	d, err := ParseDisplay(os.Getenv("DISPLAY"))
	if err != nil {
		return nil, err
	}
	var conn net.Conn
	if d.TCP != "" {
		conn, err = net.DialTimeout("tcp", d.TCP, dialTimeout)
	} else {
		conn, err = net.DialTimeout("unix", d.Socket, dialTimeout)
	}
	if err != nil {
		trace("dial %s: %v", displayHint(d), err)
		return nil, fmt.Errorf("X-сервер %q недоступен: %w", displayHint(d), err)
	}
	trace("dial %s ok", displayHint(d))
	cl := &Client{conn: conn}
	if err := cl.handshake(d, xauthCookie()); err != nil {
		conn.Close()
		return nil, err
	}
	// Рукопожатие окончено — постоянный дедлайн снимаем: в steady-state
	// каждый последующий read получает свой дедлайн внутри readFull.
	conn.SetReadDeadline(time.Time{})
	return cl, nil
}

// dialTimeout и handshakeTimeout — границы, за которые локальный X-сервер
// обязан ответить; их отсутствие превращало CI в вечное ожидание.
const (
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 20 * time.Second
)

func displayHint(d Display) string {
	if d.TCP != "" {
		return d.TCP
	}
	return d.Socket
}

// xauthCookie — cookie из $XAUTHORITY (или ~/.Xauthority). Не нашёл — пустой:
// локальные сессии (GNOME/KDE на X11) принимают соединение по пир-контентам.
func xauthCookie() Cookie {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + "/.Xauthority"
		}
	}
	d, err := ParseDisplay(os.Getenv("DISPLAY"))
	if err != nil || path == "" {
		return Cookie{}
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		trace("xauth %s: %v", path, rerr)
		return Cookie{}
	}
	c, err := ParseXauthority(path, d)
	if err == nil {
		trace("xauth %s: cookie «%s» %d bytes (структура)", path, c.Name, len(c.Data))
		return c
	}
	if c, ok := xauthScan(raw, d); ok {
		trace("xauth %s: cookie «%s» %d bytes (поиск по строке)", path, c.Name, len(c.Data))
		return c
	}
	trace("xauth %s: %v (и структурный разбор, и поиск не нашли)", path, err)
	return Cookie{}
}

const byteOrder = 12 // LittleEndian — как в X.h: 12 = «little endian byte order»

// handshake — рукопожатие X11: порядок байт, версии протокола, имя метода
// аутентификации и данные. Дальше читаем ответ (1 байт успеха + setup-заголовок).
func (c *Client) handshake(d Display, ck Cookie) error {
	c.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	major := uint16(11)
	minor := uint16(0)
	name := []byte("MIT-MAGIC-COOKIE-1")
	data := ck.Data
	if len(data) == 0 {
		name = []byte{}
	}
	// Layout запроса по Xproto 9.2 Connection Setup Request:
	// byte-order(1) + pad(1) + major(2) + minor(2) + bytes-to-follow(2),
	// затем блоки имени и данных метода аутентификации: u16-длина (big-
	// endian) + строка + нули до кратности 4. Находка CI (EOF сразу после
	// записи):layout «11-байтный заголовок с длинами name/data» сервер
	// читал как bytes-to-follow=18 — аутентификация ломалась, сервер
	// закрывал соединение. ВАЖНО: все целые запроса рукопожатия — в той
	// байтовой последовательности, которую объявил флаг byteOrder (у нас
	// 12 = LSBFirst → little-endian). Big-endian длины сервер читает как
	// bytes-to-follow=10240 и обрывает соединение (EOF на CI-трейсе).
	// blocks по Xproto: u16-длина (LSBFirst) + СТРОКА, дополненная нулями
	// до кратности 4 (padding относится к строке, НЕ к паре «длина+строка»:
	// блок = 2 + pad4(len); на CI-EOF был от блока на 2 байта короче).
	blocks := func(s []byte) []byte {
		pad := (len(s) + 3) & ^3
		b := binary.LittleEndian.AppendUint16(nil, uint16(len(s)))
		b = append(b, s...)
		for n := len(s); n < pad; n++ {
			b = append(b, 0)
		}
		return b
	}
	nb, db := blocks(name), blocks(data)
	req := make([]byte, 0, 8+len(nb)+len(db))
	req = append(req, byteOrder, 0)
	req = binary.LittleEndian.AppendUint16(req, major)
	req = binary.LittleEndian.AppendUint16(req, minor)
	req = binary.LittleEndian.AppendUint16(req, uint16(len(nb)+len(db))) // bytes-to-follow
	req = append(req, nb...)
	req = append(req, db...)
	trace("auth: method=%q cookie=%d bytes", name, len(data))
	if _, err := c.conn.Write(req); err != nil {
		return fmt.Errorf("X-рукопожатие: запись: %w", err)
	}
	trace("request sent (%d bytes)", len(req))
	first := make([]byte, 1)
	if _, err := io.ReadFull(c.conn, first); err != nil {
		trace("no first byte: %v", err)
		return fmt.Errorf("X-рукопожатие: чтение: %w", err)
	}
	trace("first byte = %d (0 failed / 1 success / 2 authenticate)", first[0])
	switch first[0] {
	case 0: // Failed
		var h [8]byte
		io.ReadFull(c.conn, h[:])
		reasonLen := int(binary.LittleEndian.Uint16(h[4:6]))
		buf := make([]byte, reasonLen)
		io.ReadFull(c.conn, buf)
		return fmt.Errorf("X-сервер не принял нас: %s (нужен локальный X-сервер, не Wayland)", buf)
	case 1: // Success
		// Setup-ответ по спецификации (Xproto, Connection Setup Reply):
		// после байта успеха — 18 байт заголовка: release(2) rid_base(4)
		// motion(2) numScreens(2) vendorLen(2) maxReq(2) minKC(1) maxKC(1)
		// numFormats(1) pad(1). Дальше — resources (всегда 2 записи ×8),
		// имя вендора (pad4), SCREEN-записи ×32, FORMATS ×8, и для каждого
		// экрана имя корня: длина(2)+pad4. Прочитать и пропустить НУЖНО ЦЕЛИКОМ:
		// leftover-байты setup-ответа, попав в readFull следующих запросов,
		// сдвигают рамки пакетов, и клиент навечно зависает в io.ReadFull
		// (SYMPTOM на xvfb: focusdbg/focuswatch не печатают ни строки).
		hdr := make([]byte, 18)
		if _, err := io.ReadFull(c.conn, hdr); err != nil {
			return fmt.Errorf("X-рукопожатие: setup-заголовок: %w", err)
		}
		ridBase := binary.LittleEndian.Uint32(hdr[2:6])
		c.rootID = ridBase + 0x1ff // id root-окна первого экрана (как в Xlib)
		numScreens := int(binary.LittleEndian.Uint16(hdr[8:10]))
		vendorLen := int(binary.LittleEndian.Uint16(hdr[10:12]))
		numFormats := int(hdr[16])
		trace("setup header: release=%d rid_base=%x screens=%d vendor=%d req=%d formats=%d",
			binary.LittleEndian.Uint16(hdr[0:2]), ridBase, numScreens, vendorLen,
			binary.LittleEndian.Uint16(hdr[12:14]), numFormats)
		pad4 := func(n int) int { return (n + 3) & ^3 }
		skip := make([]byte, 16+pad4(vendorLen)+numScreens*32+numFormats*8)
		if _, err := io.ReadFull(c.conn, skip); err != nil {
			trace("setup data (%d bytes): %v", len(skip), err)
			return fmt.Errorf("X-рукопожатие: setup-данные: %w", err)
		}
		trace("setup data skipped (%d bytes)", len(skip))
		nl := make([]byte, 2)
		for i := 0; i < numScreens; i++ {
			if _, err := io.ReadFull(c.conn, nl); err != nil {
				trace("root name %d: %v", i, err)
				return fmt.Errorf("X-рукопожатие: имя корня: %w", err)
			}
			n := int(binary.LittleEndian.Uint16(nl))
			name := make([]byte, pad4(n))
			if _, err := io.ReadFull(c.conn, name); err != nil {
				trace("root name %d (%d bytes): %v", i, n, err)
				return fmt.Errorf("X-рукопожатие: имя корня: %w", err)
			}
		}
		trace("setup done: root=%x", c.rootID)
		return nil
	case 2: // Authenticate
		return errors.New("X-сервер просит аутентификацию, которой у нас нет (нужен XAUTHORITY)")
	default:
		return fmt.Errorf("X-рукопожатие: неожиданный ответ %d", first[0])
	}
}
