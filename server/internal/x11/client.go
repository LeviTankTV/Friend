package x11

import (
	"encoding/binary"
	"io"
	"net"
	"time"
)

// readTimeout — сколько ждём ОДИН пакет от локального X-сервера. Без дедлайна
// зависший X (CI-xvfb) превращал io.ReadFull в вечное ожидание: помощник не
// печатал ни строки и умирал только от внешнего timeout.
const readTimeout = 10 * time.Second

// Клиентские константы протокола (Xproto, глава «Packet Types»).
// Порядок байт полей пакетов — little-endian (byteOrder в рукопожатии).
const (
	opInternAtom  = 19
	opGetProperty = 20
	opGetGeometry = 15
	opQueryExt    = 131

	// Типи пакетов сервера (Xproto «Generic Reply/Error/Event»):
	//   0 = Error, 1 = Reply, 2 = KeymapNotify (событие 24 байта, без seq),
	//   3..127 = события (32 байта, seq = номер последнего запроса),
	//   ≥128 = ответы на SendEvent (мы их не шлём).
	rtError      = 0
	rtReply      = 1
	rtKeymapEv   = 2
	keymapPacket = 24

	typeAtom = 4 // ATOM (список атомов в 32-битных слотах)

	PropActive = "_NET_ACTIVE_WINDOW"
	PropName   = "_NET_WM_NAME"
	PropPID    = "_NET_WM_PID"
	PropClass  = "WM_CLASS"

	// Имя расширения простоя (MIT-SCREEN-SAVER), чей opcode запрашиваем.
	extScreenSaver = "MIT-SCREEN-SAVER"
)

// Client — соединение с X-сервером. Протокол блокирующий (запрос → ответ),
// помощник однопоточен и владеет соединением один.
type Client struct {
	conn   net.Conn
	seq    uint16
	rootID uint32 // id root-окна первого экрана (из setup-ответа, как в Xlib)
}

// Root — id root-окна: Xlib считает его как rid_base + 0x1ff. Хардкод 0x1ff
// работал только на старых Xvfb с rid_base=0x200; современный Xvfb отдаёт
// rid_base=0x200000, и запрос к 0x1ff получает BadWindow.
func (c *Client) Root() uint32 { return c.rootID }

// Close закрывает соединение.
func (c *Client) Close() error { return c.conn.Close() }

// request пишет пакет запроса: opcode, 0, длина данных в 4-байтных словах,
// seq.
func (c *Client) request(opcode byte, data []byte) {
	c.seq++
	pkt := make([]byte, 0, 4+len(data))
	pkt = append(pkt, opcode, 0)
	pkt = binary.BigEndian.AppendUint16(pkt, uint16(len(data)/4))
	pkt = binary.BigEndian.AppendUint16(pkt, c.seq)
	pkt = append(pkt, data...)
	c.conn.Write(pkt)
}

// readFull читает ОДИН пакет сервера целиком, зная его длину из типа:
// Error 32, Reply 32+extra, KeymapNotify 24 (без seq!), события 32.
// Рамки по типу обязательны: KeymapNotify (первое же событие при WM с
// grabbed-клавиатурой) сдвигает поток на 8 байт, и слепое чтение «по 32»
// навечно уходит в io.ReadFull.
func (c *Client) readFull() (rt byte, header []byte, seq uint16, extra []byte, err error) {
	c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	defer c.conn.SetReadDeadline(time.Time{})
	t := make([]byte, 1)
	if _, err = io.ReadFull(c.conn, t); err != nil {
		return 0, nil, 0, nil, err
	}
	if t[0] == rtKeymapEv {
		rest := make([]byte, keymapPacket-1)
		if _, err = io.ReadFull(c.conn, rest); err != nil {
			return 0, nil, 0, nil, err
		}
		return rtKeymapEv, rest, 0, nil, nil
	}
	h := make([]byte, 32)
	h[0] = t[0]
	if _, err = io.ReadFull(c.conn, h[1:]); err != nil {
		return 0, nil, 0, nil, err
	}
	rt = h[0]
	seq = binary.LittleEndian.Uint16(h[2:4])
	if rt == rtReply {
		n := int(binary.LittleEndian.Uint32(h[4:8]))
		extra = make([]byte, n*4)
		if len(extra) > 0 {
			if _, err = io.ReadFull(c.conn, extra); err != nil {
				return 0, nil, 0, nil, err
			}
		}
	}
	return rt, h, seq, extra, nil
}

// reply — запрос с ответом: пишем и читаем пакеты, пропуская события
// (KeymapNotify, PropertyNotify, …) и ответы на чужие seq, пока не придёт
// пакет НАШЕГО типа по НАШЕЙ последовательности.
func (c *Client) reply(opcode byte, data []byte) (seq uint16, header, extra []byte, err error) {
	c.request(opcode, data)
	for {
		rt, h, s, ex, err := c.readFull()
		if err != nil {
			return 0, nil, nil, err
		}
		switch {
		case rt == rtReply && s == c.seq:
			return s, h, ex, nil
		case rt == rtError && s == c.seq:
			// Окно мертво / свойства нет — для помощника это не сбой:
			// пустой ответ (nil) = «данных сейчас нет».
			return s, nil, nil, nil
		default:
			// Событие (их seq = номер последнего запроса, совпадает с
			// c.seq по построению!) или ответ прошлых запросов — пропускаем.
			continue
		}
	}
}
