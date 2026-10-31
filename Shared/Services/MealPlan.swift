import Foundation
import SwiftData

/// Меню на неделю: что готовим, чего на это не хватит и что уже приготовлено.
enum MealPlan {

    static func add(_ recipe: Recipe, on day: Date, servings: Int, in context: ModelContext) {
        context.insert(PlannedMeal(day: day, recipe: recipe, servings: servings))
        try? context.save()
    }

    /// Сколько всего нужно на все блюда разом — одно и то же молоко для
    /// омлета и блинов складывается, а не проверяется дважды по отдельности.
    static func shortages(for meals: [PlannedMeal], stock: StockSnapshot) -> [Shortage] {
        var total: [String: IngredientRequirement] = [:]
        var order: [String] = []
        for meal in meals where !meal.isCooked {
            guard let recipe = meal.recipe else { continue }
            for requirement in Kitchen.requirements(for: recipe, servings: meal.servings) {
                if let existing = total[requirement.productName] {
                    total[requirement.productName] = IngredientRequirement(
                        productName: requirement.productName, amount: existing.amount + requirement.amount,
                        unit: requirement.unit)
                } else {
                    total[requirement.productName] = requirement
                    order.append(requirement.productName)
                }
            }
        }
        guard case .missing(let shortages) = RecipeAvailability.status(for: order.compactMap { total[$0] },
                                                                          stock: stock) else { return [] }
        return shortages
    }

    /// Блюда, которые ещё впереди: сегодня и дальше, не приготовленные.
    static func upcoming(in context: ModelContext, from now: Date = .now) -> [PlannedMeal] {
        let today = Calendar.current.startOfDay(for: now)
        let all = (try? context.fetch(FetchDescriptor<PlannedMeal>())) ?? []
        return all.filter { !$0.isCooked && $0.day >= today }
    }

    /// Приготовили по рецепту — отмечаем ближайшее такое блюдо в меню:
    /// сегодняшнее или пропущенное раньше.
    static func markCooked(_ recipe: Recipe, in context: ModelContext, now: Date = .now) {
        let today = Calendar.current.startOfDay(for: now)
        let meals = ((try? context.fetch(FetchDescriptor<PlannedMeal>())) ?? [])
            .filter { !$0.isCooked && $0.day <= today && $0.recipe?.persistentModelID == recipe.persistentModelID }
            .sorted { $0.day > $1.day }
        meals.first?.isCooked = true
    }
}
