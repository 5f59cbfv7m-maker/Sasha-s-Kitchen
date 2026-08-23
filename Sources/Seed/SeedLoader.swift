import Foundation
import SwiftData

/// Первичное наполнение базы. Идемпотентно: если каталог уже не пуст, ничего
/// не делает, поэтому вызывать можно на каждом запуске.
enum SeedLoader {

    @discardableResult
    static func loadIfNeeded(into context: ModelContext) -> Bool {
        let existing = (try? context.fetchCount(FetchDescriptor<Product>())) ?? 0
        guard existing == 0 else { return false }
        load(into: context)
        return true
    }

    /// Полная перезагрузка: стирает всё и раскладывает стартовые данные заново.
    static func reset(_ context: ModelContext) {
        for model in [CookingLog.self] { try? context.delete(model: model) }
        try? context.delete(model: RecipeIngredient.self)
        try? context.delete(model: Recipe.self)
        try? context.delete(model: StockItem.self)
        try? context.delete(model: Product.self)
        load(into: context)
    }

    static func load(into context: ModelContext) {
        var catalog: [String: Product] = [:]
        let today = Date.now

        for seed in SeedData.products {
            let product = Product(
                name: seed.name, category: seed.category, unit: seed.unit,
                gramsPerUnit: seed.gramsPerUnit,
                caloriesPer100: seed.caloriesPer100, proteinPer100: seed.proteinPer100,
                fatPer100: seed.fatPer100, carbsPer100: seed.carbsPer100,
                lowStockThreshold: seed.lowStockThreshold,
                shelfLifeDays: seed.shelfLifeDays)
            context.insert(product)
            catalog[seed.name] = product

            // Покупка «состоялась» несколько дней назад, чтобы сроки годности
            // выглядели живыми уже на первом экране.
            let purchase = today.addingTimeInterval(-Double(seed.purchasedDaysAgo) * 86_400)
            let expiry = seed.shelfLifeDays.map {
                purchase.addingTimeInterval(Double($0) * 86_400)
            }
            let stock = StockItem(product: product,
                                  currentAmount: seed.currentAmount,
                                  initialAmount: seed.initialAmount,
                                  purchaseDate: purchase,
                                  expiryDate: seed.currentAmount > 0 ? expiry : nil)
            context.insert(stock)
        }

        for seed in SeedData.recipes {
            let recipe = Recipe(name: seed.name,
                                cookingTimeMinutes: seed.cookingTimeMinutes,
                                baseServings: seed.baseServings)
            context.insert(recipe)
            for (index, item) in seed.ingredients.enumerated() {
                guard let product = catalog[item.product] else {
                    assertionFailure("Рецепт «\(seed.name)»: нет продукта «\(item.product)»")
                    continue
                }
                let ingredient = RecipeIngredient(product: product,
                                                  amountPerBaseServing: item.amountPerBaseServing,
                                                  order: index)
                context.insert(ingredient)
                ingredient.recipe = recipe
                recipe.ingredients.append(ingredient)
            }
        }

        try? context.save()
    }
}
