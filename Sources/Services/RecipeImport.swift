import Foundation
import SwiftData
import Vision

/// Рецепт со страницы сайта или с фото страницы книги — черновик для
/// редактора: человек проверит граммовки и продукты, прежде чем сохранить.
struct ImportedRecipe: Identifiable {
    let id = UUID()
    var name: String
    var minutes: Int?
    var servings: Int?
    var ingredients: [ParsedIngredient]
    var steps: String
    var imageURL: URL?
    var photoData: Data?
}

/// Строка ингредиента, разобранная на имя, количество и единицу базы
/// («г», «мл», «шт»). Не разобралось количество — 0: человек впишет сам.
nonisolated struct ParsedIngredient: Equatable, Sendable {
    var name: String
    var amount: Double
    var unit: String
}

nonisolated enum RecipeImport {

    enum ImportError: LocalizedError {
        case noRecipe, unreadable

        var errorDescription: String? {
            switch self {
            case .noRecipe: String(localized: "На этой странице не нашлось рецепта. Попробуйте ссылку на сам рецепт или фото страницы.")
            case .unreadable: String(localized: "Не удалось прочитать текст на фото.")
            }
        }
    }

    // MARK: Сайт

    /// Почти все сайты рецептов размечают их для поисковиков (schema.org
    /// Recipe в JSON-LD) — её и читаем, вёрстку сайта не разбираем.
    static func fromWeb(_ url: URL) async throws -> ImportedRecipe {
        var request = URLRequest(url: url, timeoutInterval: 20)
        request.setValue("Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) SashasKitchen/1.1",
                         forHTTPHeaderField: "User-Agent")
        let (data, _) = try await URLSession.shared.data(for: request)
        guard let html = String(data: data, encoding: .utf8) ?? String(data: data, encoding: .isoLatin1),
              var recipe = parseHTML(html) else { throw ImportError.noRecipe }
        if let image = recipe.imageURL {
            recipe.photoData = try? await URLSession.shared.data(from: image).0
        }
        return recipe
    }

    static func parseHTML(_ html: String) -> ImportedRecipe? {
        let pattern = #"<script[^>]*application/ld\+json[^>]*>(.*?)</script>"#
        guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive, .dotMatchesLineSeparators])
        else { return nil }
        let range = NSRange(html.startIndex..., in: html)
        for match in regex.matches(in: html, range: range) {
            guard let body = Range(match.range(at: 1), in: html),
                  let json = try? JSONSerialization.jsonObject(with: Data(html[body].utf8)),
                  let object = findRecipe(in: json) else { continue }
            return recipe(from: object)
        }
        return nil
    }

    private static func findRecipe(in json: Any) -> [String: Any]? {
        if let array = json as? [Any] { return array.lazy.compactMap(findRecipe).first }
        guard let object = json as? [String: Any] else { return nil }
        let type = object["@type"]
        if (type as? String) == "Recipe" || (type as? [String])?.contains("Recipe") == true { return object }
        if let graph = object["@graph"] { return findRecipe(in: graph) }
        return nil
    }

    private static func recipe(from object: [String: Any]) -> ImportedRecipe? {
        guard let name = (object["name"] as? String).map(decodeEntities)?.nilIfEmpty else { return nil }
        let ingredients = (object["recipeIngredient"] as? [String] ?? object["ingredients"] as? [String] ?? [])
            .map { parseIngredient(decodeEntities($0)) }
            .filter { !$0.name.isEmpty }
        let time = ["totalTime", "cookTime", "prepTime"].lazy.compactMap { (object[$0] as? String).flatMap(minutes) }.first
        return ImportedRecipe(name: name, minutes: time, servings: servings(object["recipeYield"]),
                              ingredients: ingredients, steps: steps(object["recipeInstructions"]),
                              imageURL: imageURL(object["image"]))
    }

    /// «4», «4 servings», ["4", "4 порции"] — берём первое число.
    static func servings(_ value: Any?) -> Int? {
        if let number = value as? Int { return number }
        let text = (value as? String) ?? (value as? [Any])?.first.map { "\($0)" }
        return text.flatMap { $0.firstMatch(of: /\d+/).flatMap { Int($0.output) } }
    }

    /// ISO 8601: PT1H30M → 90.
    static func minutes(_ iso: String) -> Int? {
        guard let match = iso.firstMatch(of: /P(?:(\d+)D)?T?(?:(\d+)H)?(?:(\d+)M)?/),
              match.output.1 != nil || match.output.2 != nil || match.output.3 != nil else { return nil }
        let days = match.output.1.flatMap { Int($0) } ?? 0
        let hours = match.output.2.flatMap { Int($0) } ?? 0
        let minutes = match.output.3.flatMap { Int($0) } ?? 0
        let total = days * 1440 + hours * 60 + minutes
        return total > 0 ? total : nil
    }

    private static func steps(_ value: Any?) -> String {
        func texts(_ value: Any?) -> [String] {
            if let text = value as? String { return [text] }
            if let list = value as? [Any] { return list.flatMap(texts) }
            if let object = value as? [String: Any] {
                if let items = object["itemListElement"] { return texts(items) }
                if let text = object["text"] as? String { return [text] }
            }
            return []
        }
        return texts(value).map(decodeEntities)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .enumerated().map { "\($0.offset + 1). \($0.element)" }
            .joined(separator: "\n")
    }

    private static func imageURL(_ value: Any?) -> URL? {
        if let text = value as? String { return URL(string: text) }
        if let list = value as? [Any] { return list.lazy.compactMap(imageURL).first }
        if let object = value as? [String: Any] { return imageURL(object["url"]) }
        return nil
    }

    static func decodeEntities(_ text: String) -> String {
        var result = text
        for (entity, char) in [("&amp;", "&"), ("&quot;", "\""), ("&#39;", "'"), ("&#039;", "'"), ("&apos;", "'"),
                               ("&lt;", "<"), ("&gt;", ">"), ("&nbsp;", " "), ("&#8217;", "’"), ("&frac12;", "½")] {
            result = result.replacingOccurrences(of: entity, with: char)
        }
        return result.replacingOccurrences(of: #"<[^>]+>"#, with: "", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: Фото

    /// Текст со снимка страницы: строки сверху вниз, язык — любой из тех,
    /// что знает Vision.
    static func recognizeText(in image: CGImage) async throws -> [String] {
        try await withCheckedThrowingContinuation { continuation in
            let request = VNRecognizeTextRequest { request, error in
                if let error { return continuation.resume(throwing: error) }
                let lines = (request.results as? [VNRecognizedTextObservation] ?? [])
                    .compactMap { $0.topCandidates(1).first?.string }
                continuation.resume(returning: lines)
            }
            request.recognitionLevel = .accurate
            request.automaticallyDetectsLanguage = true
            request.usesLanguageCorrection = true
            do {
                try VNImageRequestHandler(cgImage: image).perform([request])
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }

    /// Первая строка — название; строки с количеством — ингредиенты;
    /// остальное — шаги. Человек поправит в редакторе.
    static func fromText(_ lines: [String]) -> ImportedRecipe? {
        let lines = lines.map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        guard let name = lines.first else { return nil }
        var ingredients: [ParsedIngredient] = []
        var steps: [String] = []
        for line in lines.dropFirst() {
            let parsed = parseIngredient(line)
            if parsed.amount > 0, line.count <= 60, !parsed.name.isEmpty {
                ingredients.append(parsed)
            } else {
                steps.append(line)
            }
        }
        return ImportedRecipe(name: name, minutes: nil, servings: nil, ingredients: ingredients,
                              steps: steps.joined(separator: " "))
    }

    // MARK: Строка ингредиента

    private static let fractions: [Character: String] = ["½": ".5", "¼": ".25", "¾": ".75", "⅓": ".33", "⅔": ".67", "⅛": ".125"]

    /// Единицы рецептов в разных языках → множитель и единица базы.
    private static let units: [(pattern: String, factor: Double, base: String)] = [
        ("kg|кг|kilos?|kilograms?|キロ|킬로그램|公斤|千克|กก\\.?", 1000, UnitCode.gram),
        ("g|gr|grams?|grammes?|г|гр|грамм(?:а|ов)?|グラム|그램|克|กรัม", 1, UnitCode.gram),
        ("lbs?|pounds?|фунт(?:а|ов)?", 453.6, UnitCode.gram),
        ("oz|ounces?|унци[йия]", 28.35, UnitCode.gram),
        ("fl\\.? ?oz", 29.57, UnitCode.milliliter),
        ("l|liters?|litres?|л|литр(?:а|ов)?|リットル|리터|升", 1000, UnitCode.milliliter),
        ("ml|milliliters?|millilitres?|мл|ミリリットル|밀리리터|毫升|มล\\.?", 1, UnitCode.milliliter),
        ("cups?|tazas?|tasses?|стакан(?:а|ов)?|カップ|컵|杯|ถ้วย", 240, UnitCode.milliliter),
        ("tbsps?|tablespoons?|ст\\.? ?л\\.?|столов(?:ая|ые|ых) ложк[аи]|大さじ|큰술|汤匙|ช้อนโต๊ะ|EL|c\\. ?à s\\.", 15, UnitCode.milliliter),
        ("tsps?|teaspoons?|ч\\.? ?л\\.?|чайн(?:ая|ые|ых) ложк[аи]|小さじ|작은술|茶匙|ช้อนชา|TL|c\\. ?à c\\.", 5, UnitCode.milliliter),
        ("pcs?|pieces?|шт\\.?|штук[аи]?|個|개|个|ฟอง|ลูก|cloves?|зубч(?:ик|ика|иков)", 1, UnitCode.piece),
    ]

    static func parseIngredient(_ raw: String) -> ParsedIngredient {
        var line = ""
        for char in raw { line += fractions[char] ?? String(char) }
        line = line.replacingOccurrences(of: #"\([^)]*\)"#, with: "", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let unitAlternatives = units.map(\.pattern).joined(separator: "|")
        let number = #"(\d+(?:[.,]\d+)?(?:\s+\d+/\d+)?|\d+/\d+|[.,]\d+)"#

        // «2 cups flour», «200 г муки», «1 1/2 tsp salt», «大さじ2 醤油».
        let leading = try! NSRegularExpression(pattern: "^(?:(\(unitAlternatives))\\s*)?\(number)(?:\\s*[-–]\\s*\\d+(?:[.,]\\d+)?)?\\s*(\(unitAlternatives))?(?=\\s|$|\\.)\\.?\\s*(?:of\\s+)?(.*)$",
                                              options: [.caseInsensitive])
        // «卵 2個», «牛乳 200ml», «Молоко — 200 мл», «醤油 大さじ2».
        let trailing = try! NSRegularExpression(pattern: "^(.*?)[\\s:—–-]*(?:(\(unitAlternatives))\\s*)?\(number)\\s*(\(unitAlternatives))?\\.?$",
                                               options: [.caseInsensitive])
        let range = NSRange(line.startIndex..., in: line)
        func group(_ match: NSTextCheckingResult, _ index: Int) -> String? {
            Range(match.range(at: index), in: line).map { String(line[$0]) }
        }
        if let match = leading.firstMatch(in: line, range: range), let quantity = group(match, 2) {
            return make(name: group(match, 4) ?? "", quantity: quantity, unit: group(match, 3) ?? group(match, 1))
        }
        if let match = trailing.firstMatch(in: line, range: range), let quantity = group(match, 3),
           let name = group(match, 1), !name.isEmpty {
            return make(name: name, quantity: quantity, unit: group(match, 4) ?? group(match, 2))
        }
        return ParsedIngredient(name: cleanName(line), amount: 0, unit: UnitCode.piece)
    }

    private static func make(name: String, quantity: String, unit: String?) -> ParsedIngredient {
        var amount = 0.0
        for part in quantity.split(separator: " ") {
            if part.contains("/") {
                let pieces = part.split(separator: "/").compactMap { Double($0) }
                if pieces.count == 2, pieces[1] != 0 { amount += pieces[0] / pieces[1] }
            } else {
                amount += Double(part.replacingOccurrences(of: ",", with: ".")) ?? 0
            }
        }
        guard let unit, let known = units.first(where: { unit.wholeMatch(of: try! Regex($0.pattern).ignoresCase()) != nil })
        else { return ParsedIngredient(name: cleanName(name), amount: amount, unit: UnitCode.piece) }
        return ParsedIngredient(name: cleanName(name), amount: (amount * known.factor * 10).rounded() / 10, unit: known.base)
    }

    /// «garlic, minced» → «garlic»; «муки пшеничной» — как есть, сопоставит каталог.
    private static func cleanName(_ name: String) -> String {
        let head = name.split(separator: ",", maxSplits: 1).first.map(String.init) ?? name
        return head.trimmingCharacters(in: CharacterSet(charactersIn: " :—–-.").union(.whitespacesAndNewlines))
    }
}

extension RecipeImport {
    /// Продукт каталога для строки рецепта: то же имя, или одно имя внутри
    /// другого («мука» ↔ «Мука пшеничная»). Нет такого — новый продукт без
    /// КБЖУ; редактор удалит его, если рецепт не сохранят.
    @MainActor
    static func product(for ingredient: ParsedIngredient, in context: ModelContext) -> (product: Product, amount: Double, isNew: Bool) {
        let products = (try? context.fetch(FetchDescriptor<Product>())) ?? []
        let name = ingredient.name
        let match = products.first { $0.name.sameName(as: name) }
            ?? products.filter { $0.name.localizedStandardContains(name) || name.localizedStandardContains($0.name) }
                .max { $0.name.count < $1.name.count }
        guard let match else {
            let product = Product(name: name.prefix(1).uppercased() + name.dropFirst(), category: "Прочее",
                                  unit: ingredient.unit, gramsPerUnit: ingredient.unit == UnitCode.piece ? 100 : 1,
                                  caloriesPer100: 0, proteinPer100: 0, fatPer100: 0, carbsPer100: 0, isCustom: true)
            context.insert(product)
            return (product, ingredient.amount, true)
        }
        return (match, convert(ingredient, to: match), false)
    }

    /// Граммы в штуки и обратно — через вес штуки; миллилитры считаем
    /// граммами (для кухни точности хватает).
    @MainActor
    private static func convert(_ ingredient: ParsedIngredient, to product: Product) -> Double {
        let amount = ingredient.amount
        switch (ingredient.unit, product.unit) {
        case let (from, to) where from == to: return amount
        case (UnitCode.piece, _): return amount * product.gramsPerUnit
        case (_, UnitCode.piece): return max(1, (amount / max(1, product.gramsPerUnit)).rounded())
        default: return amount
        }
    }
}
