import Foundation

/// Сведения о продукте по штрихкоду из открытой базы Open Food Facts
/// (без ключа и регистрации). Наружу уходит только сам штрихкод.
nonisolated enum FoodFacts {

    struct Info: Sendable, Equatable {
        var barcode: String
        var name: String
        var brand: String?
        var quantity: String?
        var imageURL: URL?
        /// КБЖУ на 100 г; nil, если база их не знает.
        var per100: Nutrition?
    }

    enum LookupError: LocalizedError {
        case badBarcode, notFound, network

        var errorDescription: String? {
            switch self {
            case .badBarcode: String(localized: "Это не похоже на штрихкод: нужны 8–14 цифр.")
            case .notFound: String(localized: "Такого штрихкода нет в базе Open Food Facts.")
            case .network: String(localized: "Не удалось связаться с Open Food Facts. Проверьте интернет.")
            }
        }
    }

    /// Штрихкод уходит в адрес запроса, поэтому пропускаем только цифры.
    static func normalized(_ raw: String) -> String? {
        let digits = raw.filter(\.isWholeNumber)
        guard digits.count == raw.trimmingCharacters(in: .whitespaces).count,
              (8...14).contains(digits.count) else { return nil }
        return digits
    }

    static func lookup(_ raw: String, language: String = Locale.current.language.languageCode?.identifier ?? "en")
        async throws -> Info {
        guard let code = normalized(raw) else { throw LookupError.badBarcode }
        let fields = "product_name,product_name_\(language),brands,quantity,image_front_small_url,nutriments"
        let url = URL(string: "https://world.openfoodfacts.org/api/v2/product/\(code).json?fields=\(fields)")!
        var request = URLRequest(url: url, timeoutInterval: 15)
        // Open Food Facts просит представляться.
        request.setValue("SashasKitchen/1.1 (iOS; github.com/5f59cbfv7m-maker/Sasha-s-Kitchen)",
                         forHTTPHeaderField: "User-Agent")
        let data: Data
        do {
            (data, _) = try await URLSession.shared.data(for: request)
        } catch {
            throw LookupError.network
        }
        guard let info = parse(data, barcode: code, language: language) else { throw LookupError.notFound }
        return info
    }

    static func parse(_ data: Data, barcode: String, language: String) -> Info? {
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              (root["status"] as? Int) == 1,
              let product = root["product"] as? [String: Any] else { return nil }

        func text(_ key: String) -> String? {
            (product[key] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
        }
        guard let name = text("product_name_\(language)") ?? text("product_name") else { return nil }

        var per100: Nutrition?
        if let n = product["nutriments"] as? [String: Any] {
            func value(_ key: String) -> Double? { (n[key] as? NSNumber)?.doubleValue }
            if let kcal = value("energy-kcal_100g") {
                per100 = Nutrition(calories: kcal, protein: value("proteins_100g") ?? 0,
                                   fat: value("fat_100g") ?? 0, carbs: value("carbohydrates_100g") ?? 0)
            }
        }
        // «Brand A, Brand B» — достаточно первого.
        let brand = text("brands")?.split(separator: ",").first.map {
            $0.trimmingCharacters(in: .whitespaces)
        }
        return Info(barcode: barcode, name: name, brand: brand, quantity: text("quantity"),
                    imageURL: text("image_front_small_url").flatMap(URL.init(string:)),
                    per100: per100)
    }
}

nonisolated extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
