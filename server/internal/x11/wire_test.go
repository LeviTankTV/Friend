package x11

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Парсер DISPLAY: локальный сокет, localhost по TCP, чужой хост — отказ.
func TestParseDisplay(t *testing.T) {
	cases := []struct {
		in     string
		socket string
		tcp    string
		number int
		screen int
		wantOK bool
	}{
		{":0", "/tmp/.X11-unix/X0", "", 0, 0, true},
		{":10", "/tmp/.X11-unix/X10", "", 10, 0, true},
		{":10.1", "/tmp/.X11-unix/X10", "", 10, 1, true},
		{"localhost:0", "", "localhost:6000", 0, 0, true},
		{"", "", "", 0, 0, false},
		{"unix:0", "", "", 0, 0, false},
		{":x", "", "", 0, 0, false},
	}
	for _, c := range cases {
		d, err := ParseDisplay(c.in)
		ok := err == nil
		if ok != c.wantOK {
			t.Fatalf("DISPLAY %q: ok=%v err=%v", c.in, ok, err)
		}
		if !ok {
			continue
		}
		if d.Socket != c.socket || d.TCP != c.tcp || d.Number != c.number || d.Screen != c.screen {
			t.Fatalf("DISPLAY %q: %+v", c.in, d)
		}
	}
}

// buildXauth — запись Xauthority по формату (family u16 + четыре строки u32).
func buildXauth(t *testing.T, family uint16, addr, num, name string, data []byte) []byte {
	t.Helper()
	s := func(v string) []byte {
		b := binary.LittleEndian.AppendUint32(nil, uint32(len(v)))
		return append(b, []byte(v)...)
	}
	out := binary.LittleEndian.AppendUint16(nil, family)
	out = append(out, s(addr)...)
	out = append(out, s(num)...)
	out = append(out, s(name)...)
	out = append(out, s(string(data))...)
	return out
}

func TestParseXauthorityLocalCookie(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xauth")
	cookie := make([]byte, 16)
	for i := range cookie {
		cookie[i] = byte(i + 1)
	}
	rec := buildXauth(t, familyLocal, "Tima-PC", "0", "MIT-MAGIC-COOKIE-1", cookie)
	if err := os.WriteFile(path, rec, 0600); err != nil {
		t.Fatal(err)
	}
	d, _ := ParseDisplay(":0")
	got, err := ParseXauthority(path, d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "MIT-MAGIC-COOKIE-1" || len(got.Data) != 16 || got.Data[3] != 4 {
		t.Fatalf("cookie %+v", got)
	}
}

func TestParseXauthoritySkipsOtherDisplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xauth")
	rec := buildXauth(t, familyLocal, "", "1", "MIT-MAGIC-COOKIE-1", []byte{7})
	if err := os.WriteFile(path, rec, 0600); err != nil {
		t.Fatal(err)
	}
	d, _ := ParseDisplay(":0")
	if _, err := ParseXauthority(path, d); err == nil {
		t.Fatal("для :0 запись про :1 годиться не должна")
	}
	d1, _ := ParseDisplay(":1")
	if _, err := ParseXauthority(path, d1); err != nil {
		t.Fatalf("для :1 cookie обязан найтись: %v", err)
	}
}

// setupResponse — setup-ответ сервера по спецификации (Xproto, Connection
// Setup Reply): 18 байт заголовка, resources (2×8), вендор (pad4),
// SCREEN-записи ×32, FORMATS ×8, имя корня (len u16 + pad4).
func setupResponse(ridBase uint32) []byte {
	b := []byte{1} // Success
	b = binary.LittleEndian.AppendUint16(b, 11)
	b = binary.LittleEndian.AppendUint32(b, ridBase)
	b = binary.LittleEndian.AppendUint16(b, 0)   // motion
	b = binary.LittleEndian.AppendUint16(b, 1)   // numScreens
	b = binary.LittleEndian.AppendUint16(b, 4)   // vendorLen
	b = binary.LittleEndian.AppendUint16(b, 65535)
	b = append(b, 8, 255, 0, 0) // minKC, maxKC, numFormats, pad
	b = append(b, make([]byte, 16)...)
	b = append(b, []byte("X11\x00")...)
	b = append(b, make([]byte, 32)...) // один SCREEN
	b = binary.LittleEndian.AppendUint16(b, 8)
	b = append(b, []byte("screen 0")...)
	return b
}

// readHandshakeRequest читает запрос рукопожатия с net.Pipe целиком.
// t не передаём: вызывается из серверного goroutine и при ошибке просто
// обрывает чтение (клиентская сторона получит её сама и покажет).
func readHandshakeRequest(s net.Conn) {
	h := make([]byte, 11)
	if _, err := io.ReadFull(s, h); err != nil {
		return
	}
	nl := int(binary.LittleEndian.Uint16(h[4:6]))
	dl := int(binary.LittleEndian.Uint16(h[6:8]))
	pad := func(n int) int { return (n + 3) & ^3 }
	skip := make([]byte, pad(nl)+pad(dl))
	io.ReadFull(s, skip)
}

// Рукопожатие: success-ответ разбирается ПОЛНОСТЬЮ (root id = rid+0x1ff,
// setup-байты не оставляются в потоке), отказ — человекочитаемая причина.
func TestHandshakeSuccessAndFailure(t *testing.T) {
	c2, s2 := pipe()
	defer c2.Close()
	defer s2.Close()
	cl := &Client{conn: c2}
	go func() {
		readHandshakeRequest(s2)
		s2.Write(setupResponse(0x200000))
		// net.Pipe — синхронный: сначала вычитываем запрос internAtom,
		// потом досылаем KeymapNotify (24, без seq), событие с чужим seq,
		// ATOM-ответ на seq=1 (заголовок 32 + extra 4, atom=42).
		req := make([]byte, 16)
		s2.Read(req)
		keymap := make([]byte, 24)
		keymap[0] = 2
		s2.Write(keymap)
		ev := make([]byte, 32)
		ev[0] = 12
		binary.LittleEndian.PutUint16(ev[2:4], 99)
		s2.Write(ev)
		rep := make([]byte, 32)
		rep[0] = 1
		binary.LittleEndian.PutUint16(rep[2:4], 1)
		binary.LittleEndian.PutUint32(rep[4:8], 0)  // extra-слов нет
		binary.LittleEndian.PutUint32(rep[8:12], 42) // atom в собственном поле ответа
		s2.Write(rep)
	}()
	d, _ := ParseDisplay(":0")
	if err := cl.handshake(d, Cookie{Name: "MIT-MAGIC-COOKIE-1", Data: []byte{1, 2, 3, 4}}); err != nil {
		t.Fatalf("рукопожатие должно пройти: %v", err)
	}
	if cl.Root() != 0x200000+0x1ff {
		t.Fatalf("root id = rid_base + 0x1ff, получили %x", cl.Root())
	}
	// InternAtom(«X») уходит seq=1; сервер отдаёт ключмап+чужое событие+ответ.
	atom, err := cl.internAtom("X")
	if err != nil {
		t.Fatalf("internAtom: %v", err)
	}
	if atom != 42 {
		t.Fatalf("atom = %d, ждали 42", atom)
	}

	c3, s3 := pipe()
	defer c3.Close()
	defer s3.Close()
	cl3 := &Client{conn: c3}
	go func() {
		readHandshakeRequest(s3)
		msg := []byte("нет X")
		p := make([]byte, 8, 16)
		binary.LittleEndian.PutUint16(p[4:6], uint16(len(msg)))
		p = append(p, msg...)
		for len(p)%4 != 0 {
			p = append(p, 0)
		}
		s3.Write(append([]byte{0}, p...))
	}()
	err = cl3.handshake(d, Cookie{})
	if err == nil {
		t.Fatal("отказ обязан передаваться человеку")
	}
}

// Ключевые проверки протокола: разбор setup-ответа, root id и чтение
// ответа поверх KeymapNotify/чужих событий — в TestHandshakeSuccessAndFailure.
// pipe — пара полноценных потоковых сокетов для тестов протокола.
// net.Pipe НЕ годится: он «сообщение в сообщение» (каждый Write — один
// Read, без буфера), а X11-клиент читает поток мелкими io.ReadFull —
// на net.Pipe такой читатель неизбежно зависает.
func pipe() (net.Conn, net.Conn) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	type res struct {
		c net.Conn
		e error
	}
	ch := make(chan res, 1)
	go func() { c, e := ln.Accept(); ch <- res{c, e} }()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		panic(err)
	}
	r := <-ch
	if r.e != nil {
		panic(r.e)
	}
	return c, r.c
}
