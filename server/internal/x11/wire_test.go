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

// Рукопожатие: запись пакета (порядок байт, версии, длины), ответ Success
// с setup-данными, отказ — человекочитаемая причина.
func TestHandshakeSuccessAndFailure(t *testing.T) {
	c2, s2 := pipe()
	cl := &Client{conn: c2}
		go func() {
			// net.Pipe синхронный: сначала прочесть запрос рукопожатия
			// (11-байтный заголовок + имя 20 + cookie 4), затем ответить
			// Success + setup-заголовок (7 байт: у setup-RESPONSE нет
			// 32-байтного пакета — он special, сразу после байта успеха).
			buf := make([]byte, 35)
			io.ReadFull(s2, buf)
			s2.Write([]byte{1, 0, 11, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		}()
	d, _ := ParseDisplay(":0")
	if err := cl.handshake(d, Cookie{Name: "MIT-MAGIC-COOKIE-1", Data: []byte{1, 2, 3, 4}}); err != nil {
		t.Fatalf("рукопожатие должно пройти: %v", err)
	}
	c3, s3 := pipe()
	cl3 := &Client{conn: c3}
	go func() {
		// Failed(0): reason_len=5 «нет X» + padding. Запрос пустого cookie
		// — 11 байт, читаем их первым.
		buf := make([]byte, 11)
		io.ReadFull(s3, buf)
		msg := []byte("нет X")
		p := make([]byte, 8, 16)
		binary.LittleEndian.PutUint16(p[4:6], uint16(len(msg)))
		p = append(p, msg...)
		for len(p)%4 != 0 {
			p = append(p, 0)
		}
		s3.Write(append([]byte{0}, p...))
	}()
	err := cl3.handshake(d, Cookie{})
	if err == nil {
		t.Fatal("отказ обязан передаваться человеку")
	}
}

// pipe — сетевой pipe для тестов протокола (net.Pipe).
func pipe() (net.Conn, net.Conn) { return net.Pipe() }
