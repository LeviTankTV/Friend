package x11

import (
	"encoding/binary"
)

// Запросы высокого уровня: активное окно, имя окна, pid процесса,
// геометрия и простой в секундах (MIT-SCREEN-SAVER).

// internAtom — номер свойства по имени (ATOM).
func (c *Client) internAtom(name string) (uint32, error) {
	b := []byte(name)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	data := binary.LittleEndian.AppendUint32(nil, uint32(len(name)))
	data = append(data, b...)
	_, h, _, err := c.reply(opInternAtom, data)
	if err != nil || h == nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(h[8:12]), nil
}

// propRaw — GET_PROPERTY (AnyProperty, длинное чтение 2048 слотов):
// сырые байты свойства в их природной упаковке.
func (c *Client) propRaw(w, atom uint32) ([]byte, error) {
	data := binary.LittleEndian.AppendUint32(nil, w)
	data = binary.LittleEndian.AppendUint32(data, atom)
	data = binary.LittleEndian.AppendUint32(data, 0)    // Type = AnyProperty
	data = binary.LittleEndian.AppendUint32(data, 2048) // Longest
	_, h, extra, err := c.reply(opGetProperty, data)
	if err != nil || h == nil {
		return nil, err
	}
	format := h[5]
	n := int(binary.LittleEndian.Uint32(h[8:12]))
	switch format {
	case 8:
		if n > len(extra) {
			n = len(extra)
		}
		out := make([]byte, n)
		copy(out, extra[:n])
		return out, nil
	case 16:
		if n*2 > len(extra) {
			n = len(extra) / 2
		}
		out := make([]byte, n*2)
		copy(out, extra[:n*2])
		return out, nil
	case 32:
		// 32-битные значения (ATOM, CARDINAL): каждое — 4 байта little-endian.
		if n*4 > len(extra) {
			n = len(extra) / 4
		}
		out := make([]byte, n*4)
		copy(out, extra[:n*4])
		return out, nil
	}
	return nil, nil
}

// ActiveWindow — окно в фокусе (0 — фокуса нет: переключение рабочего
// стола, экран блокировки).
func (c *Client) ActiveWindow() (uint32, error) {
	atom, err := c.internAtom(PropActive)
	if err != nil {
		return 0, err
	}
	raw, err := c.propRaw(RootWindow, atom)
	if err != nil {
		return 0, err
	}
	if len(raw) < 4 {
		return 0, nil
	}
	return binary.LittleEndian.Uint32(raw[:4]), nil
}

// WindowName — заголовок окна: _NET_WM_NAME (UTF-8 STRING), если его нет —
// WM_NAME как байты (для латиницы читается как есть, кириллица в старых
// X-приложениях — редкость). Пустая строка — норма (у окон бывает пусто).
func (c *Client) WindowName(w uint32) string {
	atom, err := c.internAtom(PropName)
	if err == nil {
		if raw, err := c.propRaw(w, atom); err == nil && len(raw) > 0 {
			return stripNul(raw)
		}
	}
	wm, err := c.internAtom("WM_NAME")
	if err != nil {
		return ""
	}
	raw, err := c.propRaw(w, wm)
	if err != nil {
		return ""
	}
	return stripNul(raw)
}

// stripNul — из UTF-8 STRING: обрезаем завершающий NUL.
func stripNul(b []byte) string {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}

// WindowPID — pid процесса окна (0 — окно не сообщает pid, например
// кросс-платформенные тулкиты без _NET_WM_PID).
func (c *Client) WindowPID(w uint32) uint32 {
	atom, err := c.internAtom(PropPID)
	if err != nil {
		return 0
	}
	raw, err := c.propRaw(w, atom)
	if err != nil || len(raw) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(raw[:4])
}

// WindowGeometry — прямоугольник окна (GetGeometry): x, y, ширина, высота.
// Нулевой прямоугольник — «геометрии нет», страница не сажает никуда.
func (c *Client) WindowGeometry(w uint32) (int, int, int, int) {
	data := binary.LittleEndian.AppendUint32(nil, w)
	_, h, _, err := c.reply(opGetGeometry, data)
	if err != nil || h == nil {
		return 0, 0, 0, 0
	}
	x := int(int16(binary.LittleEndian.Uint16(h[8:10])))
	y := int(int16(binary.LittleEndian.Uint16(h[10:12])))
	ww := int(binary.LittleEndian.Uint16(h[12:14]))
	hh := int(binary.LittleEndian.Uint16(h[14:16]))
	return x, y, ww, hh
}

// screensaverOp — номер ответа расширения MIT-SCREEN-SAVER (0 — расширения
// нет: на некоторых сборках X оно не включено).
func (c *Client) screensaverOp() byte {
	b := []byte(extScreenSaver)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	data := binary.LittleEndian.AppendUint32(nil, uint32(len(extScreenSaver)))
	data = append(data, b...)
	c.request(opQueryExt, data)
	rt, h, _, _, err := c.readFull()
	if err != nil || rt == rtError || h == nil {
		return 0
	}
	return h[1] // first-event-code место: в QueryExtension ответе
	// opcode расширения лежит в b[1] (XQueryExtensionReply:
	// ai, b[1] = major_opcode).
}

// IdleSeconds — простой в секундах по MIT-SCREEN-SAVER QueryInfo.
// 0 = «данных нет»: расширения нет или запрос не прошёл.
func (c *Client) IdleSeconds() int {
	op := c.screensaverOp()
	if op == 0 {
		return 0
	}
	c.request(op, []byte{1, 0, 0, 0}) // внутри расширения op 1 = QueryInfo
	rt, h, _, _, err := c.readFull()
	if err != nil || rt == rtError || h == nil {
		return 0
	}
	idle := binary.LittleEndian.Uint32(h[8:12]) // idle_ms
	return int(idle / 1000)
}
