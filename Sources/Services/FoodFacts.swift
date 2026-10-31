import Foundation

/// Сведения о продукте по штрихкоду из открытых баз без ключа и регистрации:
/// сначала Open Food Facts (еда, а заодно косметика, корм и прочее из
/// родственных баз), если там нет — UPCitemdb (названия любых товаров, без
/// КБЖУ). Наружу уходит только сам штрихкод.
///
/// Японские (Yahoo! Shopping) и корейские (MFDS) базы требуют ключа на
/// разработчика — подключать, когда ключи будут; в России, Китае и Таиланде
/// открытых баз, кроме Open Food Facts, нет.
nonisolated enum FoodFacts {

    struct Info: Sendable, Equatable {
        var barcode: String
        var name: String
        var brand: String?
        var quantity: String?
        var imageURL: URL?
        /// КБЖУ на 100 г; nil, если база их не знает.
        var per100: Nutrition?
        var source: Source = .openFoodFacts
    }

    enum Source: String, Sendable {
        case openFoodFacts = "Open Food Facts"
        case upcItemDB = "UPCitemdb"
    }

    enum LookupError: LocalizedError {
        case badBarcode, notFound, network

        var errorDescription: String? {
            switch self {
            case .badBarcode: String(localized: "Это не похоже на штрихкод: нужны 8–14 цифр.")
            case .notFound: String(localized: "Такого штрихкода нет в открытых базах.")
            case .network: String(localized: "Не удалось связаться с базами продуктов. Проверьте интернет.")
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
        var reached = false
        // v3 с product_type=all сам переадресует в Open Beauty/Pet Food/Products Facts.
        let fields = "product_name,product_name_\(language),brands,quantity,image_front_small_url,nutriments"
        if let data = await get("https://world.openfoodfacts.org/api/v3/product/\(code)?product_type=all&lc=\(language)&fields=\(fields)") {
            reached = true
            if let info = parse(data, barcode: code, language: language) { return info }
        }
        if let data = await get("https://api.upcitemdb.com/prod/trial/lookup?upc=\(code)") {
            reached = true
            if let info = parseUPCitemdb(data, barcode: code) { return info }
        }
        throw reached ? LookupError.notFound : LookupError.network
    }

    /// Ответ сервера или nil, если до него не достучаться. 404 и 429 (лимит
    /// бесплатных запросов) — тоже ответ: «здесь не нашли».
    private static func get(_ address: String) async -> Data? {
        guard let url = URL(string: address) else { return nil }
        var request = URLRequest(url: url, timeoutInterval: 15)
        // Open Food Facts просит представляться: имя, версия, контакт.
        request.setValue("SashasKitchen/1.1 (support@keelspan.app)", forHTTPHeaderField: "User-Agent")
        return try? await URLSession.shared.data(for: request).0
    }

    /// UPCitemdb: название и картинка любого товара, без КБЖУ.
    static func parseUPCitemdb(_ data: Data, barcode: String) -> Info? {
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let item = (root["items"] as? [[String: Any]])?.first,
              let title = (item["title"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
        else { return nil }
        func text(_ key: String) -> String? {
            (item[key] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
        }
        return Info(barcode: barcode, name: title, brand: text("brand"), quantity: text("size"),
                    imageURL: (item["images"] as? [String])?.first.flatMap(URL.init(string:)),
                    per100: nil, source: .upcItemDB)
    }

    /// Страница продукта в Open Food Facts: если его там нет, сайт предложит
    /// добавить — тогда он найдётся у всех.
    static func contributeURL(for barcode: String) -> URL? {
        URL(string: "https://world.openfoodfacts.org/product/\(barcode)")
    }

    static func parse(_ data: Data, barcode: String, language: String) -> Info? {
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              // v3 — "success", старый v2 — 1.
              (root["status"] as? String) == "success" || (root["status"] as? Int) == 1,
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
