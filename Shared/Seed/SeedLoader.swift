import Foundation
import SwiftData

/// Стартовый набор своего региона (`SeedSet`). Сам при запуске не грузится —
/// с iCloud новое устройство получает данные с других, и демо-остатки
/// смешались бы с настоящими. Грузится кнопкой на пустом холодильнике.
enum SeedLoader {

    @discardableResult
    static func loadIfNeeded(into context: ModelContext, localized: Bool = true, set: SeedSet = .current) -> Bool {
        let existing = (try? context.fetchCount(FetchDescriptor<Product>())) ?? 0
        guard existing == 0 else { return false }
        load(into: context, localized: localized, set: set)
        return true
    }

    /// Полная перезагрузка: стирает всё и раскладывает стартовые данные заново.
    /// Удаляем по одному, а не `delete(model:)`: пакетное удаление идёт мимо
    /// контекста, и до iCloud — а значит, до других устройств — не доходит.
    static func reset(_ context: ModelContext, set: SeedSet = .current) {
        deleteAll(CookingLog.self, in: context)
        deleteAll(ShoppingItem.self, in: context)
        deleteAll(Recipe.self, in: context)
        deleteAll(Product.self, in: context)
        deleteAll(RecipeIngredient.self, in: context)
        deleteAll(StockItem.self, in: context)
        deleteAll(PlannedMeal.self, in: context)
        load(into: context, set: set)
    }

    private static func deleteAll<T: PersistentModel>(_ type: T.Type, in context: ModelContext) {
        for object in (try? context.fetch(FetchDescriptor<T>())) ?? [] { context.delete(object) }
    }

    /// Имена продуктов и рецептов переводятся на язык устройства (таблица
    /// `Seed`): после загрузки имя — обычные данные, как у своих продуктов.
    /// Тесты грузят исходные русские имена.
    static func load(into context: ModelContext, localized: Bool = true, set: SeedSet = .current) {
        func name(_ key: String) -> String {
            localized ? Bundle.main.localizedString(forKey: key, value: key, table: "Seed") : key
        }
        var catalog: [String: Product] = [:]
        let today = Date.now

        for seed in set.products {
            let product = Product(
                name: name(seed.name), category: seed.category, unit: seed.unit,
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
            let expiry = seed.shelfLifeDays.map { Kitchen.expiry(afterDays: $0, from: purchase) }
            let stock = StockItem(product: product,
                                  currentAmount: seed.currentAmount,
                                  initialAmount: seed.initialAmount,
                                  purchaseDate: purchase,
                                  expiryDate: seed.currentAmount > 0 ? expiry : nil)
            context.insert(stock)
        }

        for seed in set.recipes {
            let recipe = Recipe(name: name(seed.name),
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
            }
        }

        try? context.save()
    }
}
