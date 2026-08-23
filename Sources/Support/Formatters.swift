import Foundation

/// Числа в интерфейсе: «330 г», «0,5 шт», «1 250 ккал».
enum Fmt {

    nonisolated static func number(_ value: Double, maxFraction: Int = 1) -> String {
        let rounded = (value * 100).rounded() / 100
        if rounded == rounded.rounded() && abs(rounded) < 1e9 {
            return integer(Int(rounded))
        }
        var s = String(format: "%.\(maxFraction)f", rounded)
        while s.contains(".") && (s.hasSuffix("0") || s.hasSuffix(".")) {
            s.removeLast()
        }
        return s.replacingOccurrences(of: ".", with: ",")
    }

    /// Разряды неразрывным пробелом, как принято в русской типографике.
    nonisolated static func integer(_ value: Int) -> String {
        let digits = String(abs(value))
        var grouped = ""
        for (offset, ch) in digits.reversed().enumerated() {
            if offset > 0 && offset % 3 == 0 { grouped.append("\u{00A0}") }
            grouped.append(ch)
        }
        return (value < 0 ? "-" : "") + String(grouped.reversed())
    }

    nonisolated static func amount(_ value: Double, unit: String) -> String {
        "\(number(value)) \(unit)"
    }

    nonisolated static func kcal(_ value: Double) -> String {
        "\(integer(Int(value.rounded()))) ккал"
    }

    nonisolated static func grams(_ value: Double) -> String {
        "\(number(value)) г"
    }

    /// Формат для `TextField(value:format:)`. Локаль задаётся явно: стиль
    /// форматирования берёт её из себя, а не из `\.locale` окружения, поэтому
    /// без этого поле показывает «22.5» рядом с нашим «437,5».
    nonisolated static var amountStyle: FloatingPointFormatStyle<Double> {
        .number
            .precision(.fractionLength(0...1))
            .locale(Locale(identifier: "ru_RU"))
    }

    nonisolated static func minutes(_ value: Int) -> String {
        guard value >= 60 else { return "\(value) мин" }
        let h = value / 60, m = value % 60
        return m == 0 ? "\(h) ч" : "\(h) ч \(m) мин"
    }

    static let date: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ru_RU")
        f.dateFormat = "d MMMM"
        return f
    }()

    static let dateTime: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ru_RU")
        f.dateStyle = .medium
        f.timeStyle = .short
        return f
    }()
}

/// Устойчивый хеш строки. `String.hashValue` в Swift солится на каждый запуск
/// процесса, поэтому для картинок и цветов, которые не должны «мигать» между
/// запусками, берём собственный.
nonisolated func stableHash(_ string: String) -> Int {
    var hash = 5_381
    for scalar in string.unicodeScalars {
        hash = (hash &* 33) &+ Int(scalar.value)
    }
    return abs(hash)
}
