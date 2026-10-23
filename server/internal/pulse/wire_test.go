package pulse

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// Байты пакетов сверены с раскладками из исходников pulseaudio: любой сдвиг
// поля — падение теста ещё до живого сервера.

func TestBuildAuthBytes(t *testing.T) {
	cookie := append([]byte("abcd"), make([]byte, cookieLength-4)...)
	got := buildAuth(cookie, 1)

	want := &builder{}
	want.command(cmdAuth, 1)
	want.u32(protocolVersion)
	want.raw(tagArbitrary)
	want.be32(cookieLength)
	want.buf = append(want.buf, cookie...)

	if !bytes.Equal(got, want.buf) {
		t.Fatalf("AUTH не совпал:\n got  % x\n want % x", got, want.buf)
	}
	if n := len(got); n != 15+5+cookieLength {
		t.Fatalf("длина AUTH %d, ожидалась %d", n, 15+5+cookieLength)
	}
	// Рукопожатие начинается с команды 8 и тега — первые 10 байт.
	if got[0] != tagU32 || binary.BigEndian.Uint32(got[1:5]) != cmdAuth {
		t.Fatalf("первое поле не команда AUTH: % x", got[:5])
	}
}

func TestBuildRecordStreamBytes(t *testing.T) {
	src := "alsa_output.monitor"
	got := buildRecordStream(src, 2)

	want := &builder{}
	want.command(cmdCreateRecordStream, 2)
	want.sampleSpec()
	want.channelMap()
	want.u32(invalidIndex)
	want.str(src)
	want.u32(invalidIndex)
	want.boolean(false)
	want.u32(fragSize)
	for i := 0; i < 7; i++ { // флаги v12
		want.boolean(false)
	}
	want.boolean(false) // peak
	want.boolean(false) // adjust_latency
	want.proplist([][2]string{{"application.name", "friend"}})
	want.u32(0)
	want.boolean(false)
	want.boolean(false)
	want.boolean(false)
	want.u8(tagU8, 0)
	want.cvolume()
	for i := 0; i < 5; i++ {
		want.boolean(false)
	}

	if !bytes.Equal(got, want.buf) {
		t.Fatalf("CREATE_RECORD_STREAM не совпал:\n got  % x\n want % x", got, want.buf)
	}
	// Выборочная сверка ключевых мест руками (страховка от ошибки в самом
	// want): sample_spec 'a'+S16LE+mono+8000, имя источника после
	// невалидного индекса.
	if i := bytes.Index(got, []byte{'a', 3, 1, 0x00, 0x00, 0x1f, 0x40}); i < 0 {
		t.Fatalf("в пакете нет sample_spec S16LE mono 8000: % x", got)
	}
	if i := bytes.Index(got, append([]byte{tagString}, []byte(src)...)); i < 0 {
		t.Fatalf("в пакете нет имени источника %q", src)
	}
}

func TestParseServerInfoReply(t *testing.T) {
	pl := &builder{}
	pl.command(cmdReply, 10) // Conn{nextTag:10} первым тегом выдаст 10
	pl.str("tima")
	pl.str("pc")
	pl.str("15.0.0")
	pl.str("pulseaudio")
	pl.sampleSpec()
	pl.str("alsa_output_pci.analog-stereo")
	pl.str("alsa_input_pci.analog-stereo")
	pl.u8(tagU8, 42)

	c, srv := net.Pipe()
	defer c.Close()
	go func() {
		// Фейк-сервер: сначала читает запрос клиента, потом отвечает —
		// net.Pipe синхронный, два одновременных Write в него — дедлок.
		_, _ = readFrame(srv)
		_ = writeControl(srv, pl.buf)
		srv.Close()
	}()
	conn := &Conn{conn: c, nextTag: 10}
	got, err := conn.DefaultMonitor()
	if err != nil {
		t.Fatalf("DefaultMonitor: %v", err)
	}
	if want := "alsa_output_pci.analog-stereo.monitor"; got != want {
		t.Fatalf("монитор %q, ожидался %q", got, want)
	}
}

func TestParseRecordReplyChannel(t *testing.T) {
	pl := &builder{}
	pl.command(cmdReply, 10) // первый tag() у Conn{nextTag:10}
	pl.u32(7)                // канал мемблоков — первое поле
	pl.u32(11)
	pl.u32(65536)
	pl.u32(1600)

	c, srv := net.Pipe()
	defer c.Close()
	go func() {
		_, _ = readFrame(srv)
		_ = writeControl(srv, pl.buf)
		// за ответом — мемблок на канале 7: Stream.Next обязан вернуть его
		var h [frameHeaderLen]byte
		binary.BigEndian.PutUint32(h[0:4], 3)
		binary.BigEndian.PutUint32(h[4:8], 7)
		_, _ = srv.Write(h[:])
		_, _ = srv.Write([]byte{1, 2, 3})
		_ = srv.Close()
	}()
	conn := &Conn{conn: c, nextTag: 10}
	st, err := conn.Record("x.monitor")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if st.channel != 7 {
		t.Fatalf("канал %d, ожидался 7", st.channel)
	}
	block, err := st.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !bytes.Equal(block, []byte{1, 2, 3}) {
		t.Fatalf("мемблок % x, ожидался 01 02 03", block)
	}
}

func TestErrorReply(t *testing.T) {
	pl := &builder{}
	pl.command(cmdError, 5)
	pl.u32(9) // cookie не принят

	c, srv := net.Pipe()
	defer c.Close()
	go func() {
		// waitReply сам ничего не пишет — можно отвечать сразу
		_ = writeControl(srv, pl.buf)
		srv.Close()
	}()
	conn := &Conn{conn: c, nextTag: 10}
	if _, err := conn.waitReply(5); err == nil {
		t.Fatal("ожидалась ошибка cookie")
	}
}

func TestStreamKilledSurfaces(t *testing.T) {
	pl := &builder{}
	pl.command(cmdRecordStreamKilled, 0)

	c, srv := net.Pipe()
	defer c.Close()
	go func() {
		_ = writeControl(srv, pl.buf)
		srv.Close()
	}()
	st := &Stream{conn: &Conn{conn: c}, channel: 7}
	if _, err := st.Next(); err == nil {
		t.Fatal("убийство потока должно быть ошибкой")
	}
}

func TestFrameRoundTrip(t *testing.T) {
	c, srv := net.Pipe()
	defer c.Close()
	payload := buildServerInfo(1)
	go func() {
		_ = writeControl(srv, payload)
		_ = srv.Close()
	}()
	f, err := readFrame(c)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if f.channel != ctrlChannel {
		t.Fatalf("канал %x, ожидался управляющий", f.channel)
	}
	if !bytes.Equal(f.payload, payload) {
		t.Fatalf("payload не совпал: % x", f.payload)
	}
}
