# whisper_recognize.py — расшифровка WAV-записи голоса (этап 7, голосовой ввод).
#
# Работает ПОЛНОСТЬЮ локально: faster-whisper (движок CTranslate2) и модель
# лежат на машине человека; микрофонный текст никуда не отправляется.
#
# WAV читаем сами (wave + numpy), не через faster-whisper/av: запись сюда
# приходит из браузера — всегда PCM16 mono 16 кГц, наш собственный формат.
# Так цепочка не зависит от pyav: на этой машине его версия несовместима с
# faster-whisper (av.open без metadata_errors), а для своего формата он и не нужен.
#
# Аргументы: папка_модели запись.wav
# Вывод: одна строка JSON {"text": "..."} — текст без разметки, пустой если
# речи в записи не было.
import json
import sys
import wave

import numpy as np

from faster_whisper import WhisperModel


def read_pcm16_mono(wav_path: str) -> np.ndarray:
    """ PCM16 mono 16 кГц -> float32 [-1..1]. format не проверяем придирчиво:
    файл пишет наш же фронтенд, единственный источник формата."""
    with wave.open(wav_path, "rb") as w:
        if w.getnchannels() != 1:
            raise ValueError("запись не mono")
        if w.getsampwidth() != 2:
            raise ValueError("запись не 16 бит")
        frames = w.readframes(w.getnframes())
    samples = np.frombuffer(frames, dtype="<i2")
    if samples.size == 0:
        return np.zeros(0, dtype=np.float32)
    return (samples.astype(np.float32)) / 32768.0


def main() -> int:
    if len(sys.argv) != 3:
        print(json.dumps({"error": "нужны аргументы: модель и wav"}))
        return 2
    model_dir, wav_path = sys.argv[1], sys.argv[2]
    try:
        audio = read_pcm16_mono(wav_path)
        if audio.size < 1600:  # меньше десятой секунды — не речь, не гоняем модель
            print(json.dumps({"text": ""}))
            return 0
        # CPU+int8: модель base на ноутбуке расшифровывает минуту речи за
        # секунды; GPU фоновому компаньону не нужен.
        model = WhisperModel(model_dir, device="cpu", compute_type="int8")
        segments, _info = model.transcribe(
            audio,
            language="ru",
            beam_size=1,
            vad_filter=True,  # тишину не расшифровываем — она не текст
        )
        parts = [s.text.strip() for s in segments if s.text.strip()]
        print(json.dumps({"text": " ".join(parts)}, ensure_ascii=False))
        return 0
    except Exception as exc:
        print(json.dumps({"error": str(exc)}))
        return 2


if __name__ == "__main__":
    sys.exit(main())
