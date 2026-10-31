import Foundation

/// Числа, единицы и даты в интерфейсе — по правилам языка и региона
/// устройства: «0,5 шт» по-русски, «0.5 pcs» по-английски, «1 250 ккал» с
/// разрядами, как принято в этой локали. `locale` задают только тесты.
enum Fmt {

    nonisolated static func number(_ value: Double, maxFraction: Int = 1,
                                   locale: Locale = .autoupdatingCurrent) -> String {
        value.formatted(.number.precision(.fractionLength(0...maxFraction)).locale(locale))
    }

    nonisolated static func integer(_ value: Int, locale: Locale = .autoupdatingCurrent) -> String {
        value.formatted(.number.locale(locale))
    }

    /// Код единицы из базы («г», «мл», «шт») — в подпись на языке устройства.
    /// Неизвестный код (вдруг кто-то завёл свою единицу) выводится как есть.
    nonisolated static func unit(_ code: String, locale: Locale = .autoupdatingCurrent) -> String {
        localizedData(code, locale: locale)
    }

    /// Категория из базы. Стартовые («Мясо», «Овощи»…) переводятся, свои — как ввели.
    nonisolated static func category(_ name: String) -> String {
        localizedData(name)
    }

    /// Граммы и миллилитры в американских единицах показываются в oz/lb и
    /// tsp/tbsp/cups/qt — по величине. Хранится всё равно в метрических.
    nonisolated static func amount(_ value: Double, unit: String,
                                   locale: Locale = .autoupdatingCurrent) -> String {
        if UnitSystem.usesUS(locale: locale) {
            let style = Measurement<Dimension>.FormatStyle(width: .abbreviated, locale: locale, usage: .asProvided,
                                                          numberFormatStyle: .number.precision(.fractionLength(0...1)).locale(locale))
            switch unit {
            case UnitCode.gram:
                let grams = Measurement<Dimension>(value: value, unit: UnitMass.grams)
                return grams.converted(to: value >= 453.592 ? UnitMass.pounds : UnitMass.ounces).formatted(style)
            case UnitCode.milliliter:
                let target: UnitVolume = value < 14.787 ? .teaspoons : value < 59.15 ? .tablespoons
                    : value < 946.35 ? .cups : .quarts
                // Сокращение чашки — невнятное «c», поэтому чашки пишем словом.
                var volumeStyle = style
                if target == .cups { volumeStyle.width = .wide }
                return Measurement<Dimension>(value: value, unit: UnitVolume.milliliters).converted(to: target)
                    .formatted(volumeStyle)
            default:
                break
            }
        }
        return "\(number(value, locale: locale)) \(self.unit(unit, locale: locale))"
    }

    /// Единица, в которой количество вводится, и сколько в ней единиц базы:
    /// в американских — унции и жидкие унции, иначе — сама единица продукта.
    nonisolated static func inputUnit(_ code: String,
                                      locale: Locale = .autoupdatingCurrent) -> (label: String, base: Double) {
        guard UnitSystem.usesUS(locale: locale) else { return (unit(code, locale: locale), 1) }
        let formatter = MeasurementFormatter()
        formatter.locale = locale
        formatter.unitStyle = .short
        switch code {
        case UnitCode.gram: return (formatter.string(from: UnitMass.ounces), 28.349523125)
        case UnitCode.milliliter: return (formatter.string(from: UnitVolume.fluidOunces), 29.5735295625)
        default: return (unit(code, locale: locale), 1)
        }
    }

    nonisolated static func kcal(_ value: Double, locale: Locale = .autoupdatingCurrent) -> String {
        "\(integer(Int(value.rounded()), locale: locale)) \(localizedData("ккал", locale: locale))"
    }

    /// Граммы КБЖУ — граммы и в американских единицах, как на этикетках в США.
    nonisolated static func grams(_ value: Double, locale: Locale = .autoupdatingCurrent) -> String {
        "\(number(value, locale: locale)) \(unit(UnitCode.gram, locale: locale))"
    }

    /// Формат для `TextField(value:format:)`: десятичный разделитель — тот,
    /// что на клавиатуре пользователя.
    nonisolated static var amountStyle: FloatingPointFormatStyle<Double> {
        .number.precision(.fractionLength(0...1))
    }

    nonisolated static func minutes(_ value: Int, locale: Locale = .autoupdatingCurrent) -> String {
        Duration.seconds(value * 60).formatted(
            .units(allowed: [.hours, .minutes], width: .abbreviated).locale(locale))
    }

    nonisolated static func day(_ date: Date) -> String {
        date.formatted(.dateTime.day().month(.wide))
    }

    /// Строки, которые живут в базе, переводятся из таблицы `Data`.
    private nonisolated static func localizedData(_ key: String,
                                                  locale: Locale = .autoupdatingCurrent) -> String {
        guard locale != .autoupdatingCurrent else {
            return Bundle.main.localizedString(forKey: key, value: key, table: "Data")
        }
        // Явная локаль (тесты): ключи и есть русский текст, так что без
        // перевода на этот язык показываем ключ.
        guard let code = locale.language.languageCode?.identifier,
              let path = Bundle.main.path(forResource: code, ofType: "lproj"),
              let bundle = Bundle(path: path) else { return key }
        return bundle.localizedString(forKey: key, value: key, table: "Data")
    }
}

/// Метрические или американские единицы — выбор в настройках, по умолчанию
/// как в регионе устройства (американские — в США).
nonisolated enum UnitSystem: String, CaseIterable {
    case automatic, metric, us
    static let key = "unitSystem"

    static func usesUS(locale: Locale = .autoupdatingCurrent) -> Bool {
        switch UnitSystem(rawValue: UserDefaults.standard.string(forKey: key) ?? "") ?? .automatic {
        case .automatic: locale.measurementSystem == .us
        case .metric: false
        case .us: true
        }
    }
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
