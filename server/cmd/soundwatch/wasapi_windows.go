//go:build windows

// WASAPI loopback: уровень того, что играет на колонках.
//
// Граница приватности: из звукового сигнала наружу уходит РОВНО RMS-число
// (0..1). Никто не хранит и не передаёт само звучание: пакеты PCM читаются
// в цикле, из них сразу считается среднее квадратичное, и буфер отпускается.
//
// Почему чистый Go (без cgo): линкер TDM-GCC на этой машине产出ет PE с
// паттерном секций, который не может запустить ни MSYS-exec, ни сам Windows
// (WinError 193); COM-интерфейсы WASAPI — это обычные vtable-вызовы, а
// syscall.Syscall9 на Windows вызывает любой адрес с 9 параметрами.
package main

import (
	"fmt"
	"math"
	"syscall"
	"unsafe"
)

var (
	ole32              = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW  = kernel32.NewProc("CreateEventW")
)

type guid struct {
	l1 uint32
	l2 uint16
	l3 uint16
	l4 [8]byte
}

// Значения сверены с заголовками TDM-GCC (mmdeviceapi.h, audioclient.h):
// CLSID {bcde0395-…}, IID {a95664d2-…} — раньше в файле стояли перекошенные
// байты, из-за чего CoCreateInstance отвечал CLASSNOTREG (0x80040154).
var (
	clsidMMDeviceEnumerator = guid{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator  = guid{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidIAudioClient2        = guid{0x726778cd, 0xf60a, 0x4eda, [8]byte{0x82, 0xde, 0xe4, 0x76, 0x10, 0xcd, 0x78, 0xaa}}
	// Рантайм этой машины не принимает официальный IID IAudioCaptureClient
	// ({…-e840-4b7c-a3fa-008d0e0c7847}) — GetService отвечает
	// E_NOINTERFACE; принимается mingw-вариант из TDM-заголовка.
	iidIAudioCaptureClient  = guid{0xc8adbd64, 0xe71e, 0x48a0, [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
)
// ВНИМАНИЕ: заголовок TDM-GCC содержит OTHER GUID'ы для IAudioClient
// ({…-b178-c2f568a703b2}) и IAudioCaptureClient ({…-e71e-48a0-…}) —
// это вариант mingw, рантайм Windows их не принимает (E_NOINTERFACE).
// Здесь стоят официальные значения Windows SDK.

const (
	clsctxInprocServer = 1
	// В AUDCLNT_SHAREMODE SHARED = 0 (единица — это EXCLUSIVE).
	shareModeShared    = 0
	streamFlagLoopback = 0x20000  // AUDCLNT_STREAMFLAGS_LOOPBACK
	flagEventCalling   = 0x40000  // AUDCLNT_STREAMFLAGS_EVENTCALLING
	formatPCM          = 1
	formatIEEEFloat    = 3
	formatExtensible   = 0xFFFE
	flagsFormatChange  = 0x4
	flagsConversion    = 0x8
)

// vcall вызывает метод vtable: receiver + до 7 параметров.
func vcall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*8))
	switch len(args) {
	case 0:
		r, _, _ := syscall.Syscall(fn, 1, obj, 0, 0)
		return r
	case 1:
		r, _, _ := syscall.Syscall(fn, 2, obj, args[0], 0)
		return r
	case 2:
		r, _, _ := syscall.Syscall(fn, 3, obj, args[0], args[1])
		return r
	case 3:
		r, _, _ := syscall.Syscall9(fn, 4, obj, args[0], args[1], args[2], 0, 0, 0, 0, 0)
		return r
	case 4:
		r, _, _ := syscall.Syscall9(fn, 5, obj, args[0], args[1], args[2], args[3], 0, 0, 0, 0)
		return r
	case 5:
		r, _, _ := syscall.Syscall9(fn, 6, obj, args[0], args[1], args[2], args[3], args[4], 0, 0, 0)
		return r
	case 6:
		r, _, _ := syscall.Syscall9(fn, 7, obj, args[0], args[1], args[2], args[3], args[4], args[5], 0, 0)
		return r
	case 7:
		r, _, _ := syscall.Syscall9(fn, 8, obj, args[0], args[1], args[2], args[3], args[4], args[5], args[6], 0)
		return r
	}
	return 0x80070057 // E_INVALIDARG
}

// vtable-индексы IAudioClient (0..2 — IUnknown): Initialize=3,
// GetBufferSize=4, GetStreamLatency=5, GetCurrentPadding=6,
// IsFormatSupported=7, GetMixFormat=8, GetDevicePeriod=9, Start=10,
// Stop=11, Reset=12, SetEventHandle=13, GetService=14.
const (
	idxInitialize        = 3
	idxGetMixFormat      = 8
	idxStart             = 10
	idxSetEventHandle    = 13
	idxGetService        = 14
	// Порядок методов IAudioCaptureClient по TDM-заголовку:
	// GetBuffer=3, ReleaseBuffer=4, GetNextPacketSize=5.
	idxGetBuffer         = 3
	idxReleaseBuffer     = 4
	idxGetNextPacketSize = 5
)

type wasapi struct {
	capture uintptr
	float   bool
	chans   int
}

func uptr[T any](p *T) uintptr { return uintptr(unsafe.Pointer(p)) }

func (w *wasapi) initLoopback() error {
	hr, _, _ := procCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
	if hr == 0x80010106 {                     // уже инициализирован этим потоком
		hr = 0
	}
	if hr != 0 {
		return fmt.Errorf("coinitialize: 0x%08X", uint32(hr))
	}
	// CoCreateInstance сравнивает GUID'ы ПОСИМВОЛЬНО и требует именно
	// статический GUID (REFCLSID), а не адрес локальной переменной.
	var enum uintptr
	hr, _, _ = procCoCreateInstance.Call(
		uptr(&clsidMMDeviceEnumerator), 0,
		clsctxInprocServer,
		uptr(&iidIMMDeviceEnumerator),
		uptr(&enum))
	if hr != 0 {
		return fmt.Errorf("cocreateinstance: 0x%08X", uint32(hr))
	}
	var dev uintptr
	// GetDefaultAudioEndpoint(eRender=0, eConsole=1) — устройство, из
	// которого система играет.
	hr = vcall(enum, 4, 0, 1, uptr(&dev))
	if hr != 0 {
		return fmt.Errorf("getdefaultendpoint: 0x%08X", uint32(hr))
	}
	// Активируем IAudioClient2: на этой машине QI к IID_IAudioClient
	// отвечает E_NOINTERFACE, а IAudioClient2 принимает. Его vtable
	// наследует методы IAudioClient (слоты 3..14), этого достаточно.
	var cli uintptr
	hr = vcall(dev, 3, uptr(&iidIAudioClient2), clsctxInprocServer, 0, uptr(&cli))
	if hr != 0 {
		return fmt.Errorf("activate: 0x%08X", uint32(hr))
	}
	var fmtPtr uintptr
	hr = vcall(cli, idxGetMixFormat, uptr(&fmtPtr))
	if hr != 0 {
		return fmt.Errorf("getmixformat: 0x%08X", uint32(hr))
	}
	tag := *(*uint16)(unsafe.Pointer(fmtPtr)) // WAVEFORMATEX.wFormatTag
	nch := int(*(*uint16)(unsafe.Pointer(fmtPtr + 2)))
	w.float = tag == formatIEEEFloat
	if tag == formatExtensible {
		// SubFormat GUID — по смещению 18 у WAVEFORMATEXTENSIBLE; Data1 в начале.
		sub := *(*uint32)(unsafe.Pointer(fmtPtr + 22))
		w.float = sub == formatIEEEFloat
	}
	w.chans = nch
	// Initialize(ShareMode, StreamFlags, hnsBufferDuration, hnsPeriodicity,
	// pFormat, AudioSessionGuid): shared, LOOPBACK (без него захват колонки
	// отвечает UNSUPPORTED_FORMAT), буфер 1 с, периодичность 0 — для
	// лупбек-захвата периодичность не задаём, формат микшера, без сессии.
	hr = vcall(cli, idxInitialize, shareModeShared, streamFlagLoopback, 10000000, 0,
		fmtPtr, 0)
	if hr != 0 {
		return fmt.Errorf("initialize: 0x%08X", uint32(hr))
	}
	hr = vcall(cli, idxStart)
	if hr != 0 {
		return fmt.Errorf("start: 0x%08X", uint32(hr))
	}
	// GetService(IID_IAudioCaptureClient) — классический лупбек-захват
	// микшера (тот же путь, что у OBS/cpal).
	hr = vcall(cli, idxGetService, uptr(&iidIAudioCaptureClient), uptr(&w.capture))
	if hr != 0 {
		return fmt.Errorf("getservice: 0x%08X", uint32(hr))
	}
	return nil
}

func (w *wasapi) level() (float64, bool, error) {
	var n uint32
	hr := vcall(w.capture, idxGetNextPacketSize, uptr(&n))
	if hr != 0 {
		return 0, false, fmt.Errorf("wasapi: захват оборвался (getnextpacketsize 0x%08X)", uint32(hr))
	}
	var sum float64
	var cnt int64
	for n > 0 {
		var data uintptr
		var frames uint32
		var flags uint32
		var pos uint64
		var qpc uint64
		hr = vcall(w.capture, idxGetBuffer,
			uptr(&data), uptr(&frames), uptr(&flags), uptr(&pos), uptr(&qpc))
		if hr != 0 {
			return 0, false, fmt.Errorf("wasapi: захват оборвался")
		}
		if flags&(flagsFormatChange|flagsConversion) == 0 {
			samples := int64(frames) * int64(w.chans)
			if w.float {
				f := unsafe.Slice((*float32)(unsafe.Pointer(data)), samples)
				for _, v := range f {
					sum += float64(v) * float64(v)
				}
			} else {
				s := unsafe.Slice((*int16)(unsafe.Pointer(data)), samples)
				for _, v := range s {
					x := float64(v) / 32768.0
					sum += x * x
				}
			}
			cnt += samples
		}
		vcall(w.capture, idxReleaseBuffer, uintptr(n))
		hr = vcall(w.capture, idxGetNextPacketSize, uptr(&n))
		if hr != 0 {
			return 0, false, fmt.Errorf("wasapi: захват оборвался")
		}
	}
	if cnt > 0 {
		return math.Sqrt(sum / float64(cnt)), true, nil
	}
	return 0, false, nil
}

// newSource — источник звука на этой ОСи (см. main.go, interface source).
func newSource() source { return &wasapi{} }
