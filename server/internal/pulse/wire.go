// Package pulse — нативный протокол PulseAudio чистым Go (этап 8а).
//
// Зачем: танец под музыку читает уровень звука НА КОЛОНКАХ. На Windows это
// WASAPI loopback, на Linux — источник-«монитор» сервера PulseAudio (у
// PipeWire тот же путь через pipewire-pulse). Готовой Go-библиотеки без cgo
// нет, протокол простой и зафиксирован исходниками PulseAudio — пишем сами,
// тем же приёмом, что X11 (internal/x11): протокол в отдельном пакете,
// байты пакетов проверены тестами.
//
// Граница приватности прежняя (как у WASAPI-петли): из звукового сигнала
// наружу идёт РОВНО RMS-число. PCM-байты сразу сворачиваются в среднее
// квадратичное и выбрасываются — не сохраняются, не передаются, не покидают
// машину.
//
// Протокол (сверено с исходниками pulseaudio: src/pulsecore/tagstruct.{h,c},
// pstream.c, native-common.h, protocol-native.c, src/pulse/{stream,context}.c):
//   - кадр: 20 байт дескриптора, 5×uint32 BIG-ENDIAN — длина payload, канал
//     (0xFFFFFFFF = управление-тегструкт, иначе мемблок аудио), старшая и
//     младшая половины смещения, флаги (младший байт — seek-режим; SHM-биты
//     появляются только если клиент попросил SHM — мы не просим);
//   - управление: тегструкт — u32 команда, u32 тег, параметры; все скаляры
//     big-endian (htonl), перед каждым — однобайтовый тег типа;
//   - AUTH(8): u32 версия протокола, arbitrary ровно 256 байт cookie;
//   - GET_SERVER_INFO(20): в ответе имя дефолтного sink — к нему «.monitor»;
//   - CREATE_RECORD_STREAM(5): раскладка ниже (buildRecordStream); в ответе
//     канал мемблоков — ПЕРВОЕ поле;
//   - мемблоки приходят кадрами с каналом потока, payload = PCM.
package pulse

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Команды (native-common.h, позиции enum пересчитаны по исходнику).
const (
	cmdError              = 0
	cmdReply              = 2
	cmdCreateRecordStream = 5
	cmdAuth               = 8
	cmdGetServerInfo      = 20
	cmdRecordStreamKilled = 65
)

// Теги тегструкта (tagstruct.h).
const (
	tagString      = 't'
	tagStringNull  = 'N'
	tagU32         = 'L'
	tagU8          = 'B'
	tagSampleSpec  = 'a'
	tagArbitrary   = 'x'
	tagBoolTrue    = '1'
	tagBoolFalse   = '0'
	tagUsec        = 'U'
	tagChannelMap  = 'm'
	tagCvolume     = 'v'
	tagProplist    = 'P'
	tagFormatInfo  = 'f'
)

// protocolVersion — версия, которую мы объявляем (PA 15 = 35; PipeWire
// отвечает своей ≥35). Ниже 22 раскладка create_record_stream другая —
// соединение честно отклоняется (такие серверы умерли до 2016 года).
const protocolVersion = 35

const cookieLength = 256

// sampleSpec: формат PA_SAMPLE_S16LE=3, mono; 8000 Гц хватает для RMS
// (16 КБ/с), сервер сам ресемплирует под запрос.
const (
	sampleFormatS16LE = 3
	sampleRate        = 8000
	channelsMono      = 1
	channelPositionMono = 0
	volumeNorm          = 0x10000 // PA_VOLUME_NORM
	fragSize            = 1600    // 100 мс mono s16 @ 8 кГц — RMS десять раз в секунду
	invalidIndex        = 0xFFFFFFFF
)

// --- сборка тегструкта ---

type builder struct{ buf []byte }

func (b *builder) raw(tag byte)      { b.buf = append(b.buf, tag) }
func (b *builder) be32(v uint32)     { b.buf = binary.BigEndian.AppendUint32(b.buf, v) }
func (b *builder) u32(v uint32)      { b.raw(tagU32); b.be32(v) }
func (b *builder) u8(tag byte, v byte) {
	b.raw(tag)
	b.buf = append(b.buf, v)
}

func (b *builder) boolean(v bool) {
	if v {
		b.raw(tagBoolTrue)
	} else {
		b.raw(tagBoolFalse)
	}
}

func (b *builder) str(s string) {
	if s == "" {
		b.raw(tagStringNull)
		return
	}
	b.raw(tagString)
	b.buf = append(b.buf, s...)
	b.buf = append(b.buf, 0)
}

// sampleSpec: тег 'a', затем u8 формат, u8 каналы, u32 частота.
func (b *builder) sampleSpec() {
	b.raw(tagSampleSpec)
	b.buf = append(b.buf, sampleFormatS16LE, channelsMono)
	b.be32(sampleRate)
}

// channelMap: тег 'm', u8 число каналов, по u8 позиция на канал (mono=0).
func (b *builder) channelMap() {
	b.raw(tagChannelMap)
	b.buf = append(b.buf, channelsMono, channelPositionMono)
}

// arbitrary: тег 'x', u32 длина, байты.
func (b *builder) arbitrary(p []byte) {
	b.raw(tagArbitrary)
	b.be32(uint32(len(p)))
	b.buf = append(b.buf, p...)
}

// proplist: тег 'P', затем пары «строка-ключ, u32-длина, arbitrary-значение»,
// терминатор — пустая строка ('N').
func (b *builder) proplist(kv [][2]string) {
	b.raw(tagProplist)
	for _, e := range kv {
		b.str(e[0])
		b.u32(uint32(len(e[1])))
		b.arbitrary([]byte(e[1]))
	}
	b.raw(tagStringNull)
}

// cvolume: тег 'v', u8 каналы, u32 громкость на канал.
func (b *builder) cvolume() {
	b.raw(tagCvolume)
	b.buf = append(b.buf, channelsMono)
	b.be32(volumeNorm)
}

// command — начало пакета: u32 команда, u32 тег.
func (b *builder) command(cmd, tag uint32) {
	b.u32(cmd)
	b.u32(tag)
}

// buildAuth — AUTH(8): u32 версия, arbitrary 256 байт cookie. Флаги SHM и
// memfd НЕ ставим: с ними мемблоки приходят ссылками на общую память, без
// них — байтами прямо в кадре, как мы и хотим.
func buildAuth(cookie []byte, tag uint32) []byte {
	c := make([]byte, cookieLength)
	copy(c, cookie)
	b := &builder{}
	b.command(cmdAuth, tag)
	b.u32(protocolVersion)
	b.arbitrary(c)
	return b.buf
}

// buildServerInfo — GET_SERVER_INFO(20): параметров нет.
func buildServerInfo(tag uint32) []byte {
	b := &builder{}
	b.command(cmdGetServerInfo, tag)
	return b.buf
}

// buildRecordStream — CREATE_RECORD_STREAM(5) для версии ≥22, порядок полей
// сверен с обеих сторон: protocol-native.c (command_create_record_stream,
// что читает сервер) и stream.c (create_stream, что пишет клиент):
//
//	sample_spec, channel_map, u32 source_index(невалиден — ищем по имени),
//	string имя источника, u32 maxlength(дефолт), bool corked, u32 fragsize,
//	[v12] 7 bool-флагов, [v13] bool peak, bool adjust_latency, proplist,
//	u32 direct_on_input, [v14] bool early_requests, [v15] 2 bool,
//	[v22] u8 n_formats(0), cvolume, 5 bool.
func buildRecordStream(source string, tag uint32) []byte {
	b := &builder{}
	b.command(cmdCreateRecordStream, tag)
	b.sampleSpec()
	b.channelMap()
	b.u32(invalidIndex)
	b.str(source)
	b.u32(invalidIndex) // maxlength — сервер подставит дефолт
	b.boolean(false)    // corked
	b.u32(fragSize)
	b.boolean(false) // no_remap_channels
	b.boolean(false) // no_remix_channels
	b.boolean(false) // fix_format
	b.boolean(false) // fix_rate
	b.boolean(false) // fix_channels
	b.boolean(false) // no_move
	b.boolean(false) // variable_rate
	b.boolean(false) // peak_detect — нужны сами сэмплы, RMS считаем сами
	b.boolean(false) // adjust_latency — fragsize в байтах, не в микросекундах
	b.proplist([][2]string{{"application.name", "friend"}})
	b.u32(0)         // direct_on_input
	b.boolean(false) // early_requests
	b.boolean(false) // dont_inhibit_auto_suspend
	b.boolean(false) // fail_on_suspend
	b.u8(tagU8, 0)   // n_formats — нет запрошенных форматов
	b.cvolume()
	b.boolean(false) // muted
	b.boolean(false) // volume_set
	b.boolean(false) // muted_set
	b.boolean(false) // relative_volume
	b.boolean(false) // passthrough
	return b.buf
}

// --- разбор тегструкта ---

type reader struct {
	buf []byte
	pos int
}

func (r *reader) more() bool { return r.pos < len(r.buf) }

// u32 читает поле с тегом 'L'.
func (r *reader) u32() (uint32, error) {
	if !r.more() || r.buf[r.pos] != tagU32 || r.pos+5 > len(r.buf) {
		return 0, fmt.Errorf("pulse: ожидается u32 на смещении %d", r.pos)
	}
	v := binary.BigEndian.Uint32(r.buf[r.pos+1 : r.pos+5])
	r.pos += 5
	return v, nil
}

// str читает 't'-строку до нуля; 'N' — пустая.
func (r *reader) str() (string, error) {
	if !r.more() {
		return "", fmt.Errorf("pulse: ожидается строка на смещении %d", r.pos)
	}
	switch r.buf[r.pos] {
	case tagStringNull:
		r.pos++
		return "", nil
	case tagString:
		end := -1
		for i := r.pos + 1; i < len(r.buf); i++ {
			if r.buf[i] == 0 {
				end = i
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("pulse: строка без терминатора на смещении %d", r.pos)
		}
		s := string(r.buf[r.pos+1 : end])
		r.pos = end + 1
		return s, nil
	}
	return "", fmt.Errorf("pulse: ожидается строка, тег %q", r.buf[r.pos])
}

// skipSampleSpec пропускает 'a'+u8+u8+u32 (поля ответа server_info нам не
// нужны — важно только не сбиться в позиции следующего поля).
func (r *reader) skipSampleSpec() error {
	if !r.more() || r.buf[r.pos] != tagSampleSpec || r.pos+7 > len(r.buf) {
		return fmt.Errorf("pulse: ожидается sample_spec на смещении %d", r.pos)
	}
	r.pos += 7
	return nil
}

// --- кадры pstream ---

const (
	frameHeaderLen = 20
	ctrlChannel    = 0xFFFFFFFF
	maxPayload     = 1 << 20 // мемблоки у нас по ~1,6 КБ; защита от дикого кадра
)

type frame struct {
	channel uint32
	flags   uint32
	payload []byte
}

// readFrame читает один кадр: 20 байт дескриптора + payload.
func readFrame(r io.Reader) (frame, error) {
	var h [frameHeaderLen]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return frame{}, err
	}
	length := binary.BigEndian.Uint32(h[0:4])
	ch := binary.BigEndian.Uint32(h[4:8])
	flags := binary.BigEndian.Uint32(h[16:20])
	if length > maxPayload {
		return frame{}, fmt.Errorf("pulse: кадр %d байт — слишком большой", length)
	}
	p := make([]byte, length)
	if _, err := io.ReadFull(r, p); err != nil {
		return frame{}, err
	}
	return frame{channel: ch, flags: flags, payload: p}, nil
}

// writeControl отправляет тегструкт управляющим кадром (канал 0xFFFFFFFF).
func writeControl(w io.Writer, payload []byte) error {
	var h [frameHeaderLen]byte
	binary.BigEndian.PutUint32(h[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(h[4:8], ctrlChannel)
	// смещение и флаги — нули: control-кадр их не использует
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
