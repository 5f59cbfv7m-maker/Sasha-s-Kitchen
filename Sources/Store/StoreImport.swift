import Foundation
import SwiftData

/// Рецепт из магазина → в свои рецепты. Продукты сопоставляются с каталогом
/// по ключу, как на сервере (`search.NormalizeKey`); если не нашлось — через
/// переводы стартового набора: «Chicken eggs» из чужого рецепта находит ваши
/// «Яйца куриные». Чего нет совсем — создаётся из описания в bundle, с КБЖУ.
enum StoreImport {

    /// Ровно как `sk_normalize_key()` на сервере: ё→е, нижний регистр,
    /// пробелы схлопнуты и обрезаны. Расхождение молча ломает сопоставление.
    nonisolated static func key(_ name: String) -> String {
        name.replacingOccurrences(of: "Ё", with: "Е").replacingOccurrences(of: "ё", with: "е")
            .lowercased()
            .split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }

    /// Ключ любого перевода стартового продукта → его русский ключ.
    private static let seedIndex: [String: String] = {
        let keys = Set(SeedSet.allCases.flatMap { $0.products.map(\.name) })
        var index: [String: String] = [:]
        let bundles = Bundle.main.localizations.compactMap { language in
            Bundle.main.path(forResource: language, ofType: "lproj").flatMap(Bundle.init(path:))
        }
        for name in keys {
            index[key(name)] = name
            for bundle in bundles {
                index[key(bundle.localizedString(forKey: name, value: name, table: "Seed"))] = name
            }
        }
        return index
    }()

    /// Свой продукт для имени из магазина, или nil.
    static func match(_ name: String, in catalog: [String: Product]) -> Product? {
        let wanted = key(name)
        if let product = catalog[wanted] { return product }
        guard let seed = seedIndex[wanted] else { return nil }
        let local = Bundle.main.localizedString(forKey: seed, value: seed, table: "Seed")
        return catalog[key(local)] ?? catalog[key(seed)]
    }

    static func catalog(in context: ModelContext) -> [String: Product] {
        Dictionary(((try? context.fetch(FetchDescriptor<Product>())) ?? []).map { (key($0.name), $0) },
                   uniquingKeysWith: { first, _ in first })
    }

    /// Уже забирали этот рецепт?
    static func existing(_ sourceID: String, in context: ModelContext) -> Recipe? {
        ((try? context.fetch(FetchDescriptor<Recipe>())) ?? []).first { $0.storeSourceID == sourceID }
    }

    @discardableResult
    static func apply(_ bundle: StoreBundle, photo: Data?, to context: ModelContext) -> Recipe {
        if let recipe = existing(bundle.recipe.sourceId, in: context) { return recipe }
        var catalog = catalog(in: context)
        let byKey = Dictionary(bundle.products.map { ($0.key, $0) }, uniquingKeysWith: { first, _ in first })

        var amounts: [(Product, Double)] = []
        for item in bundle.ingredients.sorted(by: { $0.position < $1.position }) {
            guard let source = byKey[item.productKey] else { continue }
            let product = match(source.name, in: catalog) ?? {
                let created = Product(
                    name: source.name.split(whereSeparator: \.isWhitespace).joined(separator: " "),
                    category: source.category ?? "Прочее", unit: source.unit, gramsPerUnit: source.gramsPerUnit,
                    caloriesPer100: source.nutritionPer100.kcal, proteinPer100: source.nutritionPer100.protein,
                    fatPer100: source.nutritionPer100.fat, carbsPer100: source.nutritionPer100.carbs,
                    lowStockThreshold: source.lowStockThreshold, shelfLifeDays: source.shelfLifeDays, isCustom: true)
                context.insert(created)
                catalog[key(created.name)] = created
                return created
            }()
            let amount = RecipeImport.convert(ParsedIngredient(name: source.name, amount: item.amountPerBaseServing,
                                                               unit: source.unit), to: product)
            // Два названия из магазина могли сойтись на одном своём продукте.
            if let index = amounts.firstIndex(where: { $0.0.persistentModelID == product.persistentModelID }) {
                amounts[index].1 += amount
            } else {
                amounts.append((product, amount))
            }
        }

        let source = bundle.recipe
        var steps = source.steps.sorted { $0.order < $1.order }.enumerated()
            .map { "\($0.offset + 1). \($0.element.text)" }.joined(separator: "\n")
        steps += (steps.isEmpty ? "" : "\n\n") + String(localized: "Из магазина: \(source.author.name) (@\(source.author.handle))")
        let recipe = Recipe(name: source.title, cookingTimeMinutes: source.cookTimeMinutes + source.prepTimeMinutes,
                            baseServings: source.baseServings, steps: steps, isCustom: true, photoData: photo)
        recipe.storeSourceID = source.sourceId
        context.insert(recipe)
        for (index, item) in amounts.enumerated() {
            let ingredient = RecipeIngredient(product: item.0, amountPerBaseServing: item.1, order: index)
            context.insert(ingredient)
            ingredient.recipe = recipe
        }
        try? context.save()
        return recipe
    }
}
