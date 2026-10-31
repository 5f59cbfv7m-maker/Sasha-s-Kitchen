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

    nonisolated static func amount(_ value: Double, unit: String,
                                   locale: Locale = .autoupdatingCurrent) -> String {
        "\(number(value, locale: locale)) \(self.unit(unit, locale: locale))"
    }

    nonisolated static func kcal(_ value: Double, locale: Locale = .autoupdatingCurrent) -> String {
        "\(integer(Int(value.rounded()), locale: locale)) \(localizedData("ккал", locale: locale))"
    }

    nonisolated static func grams(_ value: Double, locale: Locale = .autoupdatingCurrent) -> String {
        amount(value, unit: UnitCode.gram, locale: locale)
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
