package x11

import (
	"encoding/binary"
	"io"
	"net"
)

// Клиентские константы протокола (X.h).
const (
	opInternAtom  = 19
	opGetProperty = 20
	opGetGeometry = 15
	opQueryExt    = 131

	rtSuccess = 0
	rtError   = 1
	rtReply   = 2

	typeAtom = 4 // ATOM (список атомов в 32-битных слотах)

	PropActive = "_NET_ACTIVE_WINDOW"
	PropName   = "_NET_WM_NAME"
	PropPID    = "_NET_WM_PID"

	RootWindow = 0x1ff // классический id root-окна: им и пользуемся

	// Имя расширения простоя (MIT-SCREEN-SAVER), чей opcode запрашиваем.
	extScreenSaver = "MIT-SCREEN-SAVER"
)

// Client — соединение с X-сервером. Протокол блокирующий (запрос → ответ),
// помощник однопоточен и владеет соединением один.
type Client struct {
	conn net.Conn
	seq  uint16
}

// Close закрывает соединение.
func (c *Client) Close() error { return c.conn.Close() }

// request пишет пакет запроса: opcode, 0, длина данных в 4-байтных словах,
// seq. Запрос без ответа (QueryExtension, QueryInfo) уходит именно так.
func (c *Client) request(opcode byte, data []byte) {
	c.seq++
	pkt := make([]byte, 0, 4+len(data))
	pkt = append(pkt, opcode, 0)
	pkt = binary.BigEndian.AppendUint16(pkt, uint16(len(data)/4))
	pkt = binary.BigEndian.AppendUint16(pkt, c.seq)
	pkt = append(pkt, data...)
	c.conn.Write(pkt)
}

// readFull читает 32-байтный заголовок пакета ответа и extra-тело reply.
// Для success/error тело нулевое (пакет ровно 32 байта).
func (c *Client) readFull() (rt byte, header []byte, seq uint16, extra []byte, err error) {
	h := make([]byte, 32)
	if _, err = io.ReadFull(c.conn, h); err != nil {
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

// reply — запрос с ответом: пишем, читаем, пока не придёт ответ НАШЕЙ
// последовательности. Ответы на чужие seq (события, ответы прошлых запросов)
// молча пропускаются: в помощнике нет асинхронных слушателей, «чужих»
// пакетов почти нет, а ring-буфер X-сервера их честно досылает.
func (c *Client) reply(opcode byte, data []byte) (seq uint16, header, extra []byte, err error) {
	c.request(opcode, data)
	for {
		rt, h, s, ex, err := c.readFull()
		if err != nil {
			return 0, nil, nil, err
		}
		if s != c.seq {
			continue
		}
		if rt == rtError {
			// Окно мертво / свойства нет — для помощника это не сбой:
			// пустой ответ (nil) = «данных сейчас нет».
			return s, nil, nil, nil
		}
		return s, h, ex, nil
	}
}
