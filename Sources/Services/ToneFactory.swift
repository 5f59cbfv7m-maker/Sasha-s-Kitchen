import Foundation

/// Короткие звуки собираются в памяти, а не лежат файлами в бандле: три-четыре
/// ноты с затуханием звучат аккуратнее системных сигналов и ничего не весят.
nonisolated enum ToneFactory {

    struct Note: Sendable {
        let frequency: Double
        let duration: Double
        var delay: Double = 0
    }

    static let sampleRate: Double = 44_100

    static func wav(_ notes: [Note], amplitude: Double = 0.32) -> Data {
        let total = notes.reduce(0.0) { $0 + $1.delay + $1.duration }
        let frameCount = max(1, Int(total * sampleRate))
        var samples = [Double](repeating: 0, count: frameCount)

        var cursor = 0.0
        for note in notes {
            cursor += note.delay
            let start = Int(cursor * sampleRate)
            let length = Int(note.duration * sampleRate)
            for i in 0..<length where start + i < frameCount {
                let t = Double(i) / sampleRate
                // Мягкая атака убирает щелчок, экспонента — «колокольчик».
                let attack = min(1, t / 0.006)
                let decay = exp(-t * 9)
                let fundamental = sin(2 * .pi * note.frequency * t)
                let harmonic = 0.22 * sin(4 * .pi * note.frequency * t)
                samples[start + i] += attack * decay * (fundamental + harmonic)
            }
            cursor += note.duration
        }

        let peak = samples.map(abs).max() ?? 1
        let gain = peak > 0 ? amplitude / peak : 0

        var pcm = Data(capacity: frameCount * 2)
        for sample in samples {
            let clamped = max(-1, min(1, sample * gain))
            pcm.appendLittleEndian(Int16(clamped * 32_767))
        }
        return riff(pcm: pcm)
    }

    private static func riff(pcm: Data) -> Data {
        let channels: UInt16 = 1
        let bitsPerSample: UInt16 = 16
        let blockAlign = channels * bitsPerSample / 8
        var data = Data()
        data.append(contentsOf: Array("RIFF".utf8))
        data.appendLittleEndian(UInt32(36 + pcm.count))
        data.append(contentsOf: Array("WAVE".utf8))
        data.append(contentsOf: Array("fmt ".utf8))
        data.appendLittleEndian(UInt32(16))          // размер fmt-чанка
        data.appendLittleEndian(UInt16(1))           // PCM без сжатия
        data.appendLittleEndian(channels)
        data.appendLittleEndian(UInt32(sampleRate))
        data.appendLittleEndian(UInt32(sampleRate) * UInt32(blockAlign))
        data.appendLittleEndian(blockAlign)
        data.appendLittleEndian(bitsPerSample)
        data.append(contentsOf: Array("data".utf8))
        data.appendLittleEndian(UInt32(pcm.count))
        data.append(pcm)
        return data
    }
}

nonisolated extension Data {
    mutating func appendLittleEndian<T: FixedWidthInteger>(_ value: T) {
        Swift.withUnsafeBytes(of: value.littleEndian) { append(contentsOf: $0) }
    }
}
