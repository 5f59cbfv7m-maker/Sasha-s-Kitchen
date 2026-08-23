import Foundation

/// Строки из starter-data.md в виде значений. `SeedData.swift` генерируется
/// скриптом Scripts/generate_seed.py, эти типы — его контракт.
nonisolated struct SeedProduct: Sendable {
    let name: String
    let category: String
    let unit: String
    let gramsPerUnit: Double
    let caloriesPer100: Double
    let proteinPer100: Double
    let fatPer100: Double
    let carbsPer100: Double
    let lowStockThreshold: Double
    /// Стартовый холодильник: сколько было куплено и сколько осталось сейчас.
    let initialAmount: Double
    let currentAmount: Double
    let shelfLifeDays: Int?
    /// Сколько дней назад «куплено» — чтобы сроки годности не совпадали у всех.
    let purchasedDaysAgo: Int
}

nonisolated struct SeedIngredient: Sendable {
    let product: String
    let amountPerBaseServing: Double
}

nonisolated struct SeedRecipe: Sendable {
    let name: String
    let cookingTimeMinutes: Int
    let baseServings: Int
    let ingredients: [SeedIngredient]
}
