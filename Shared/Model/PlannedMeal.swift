import Foundation
import SwiftData

/// Блюдо в меню на день: какой рецепт и на сколько порций.
@Model
final class PlannedMeal {
    /// Начало дня — по нему меню группируется.
    var day: Date = Date.now
    var recipe: Recipe?
    var servings: Int = 2
    var isCooked: Bool = false
    var createdAt: Date = Date.now

    init(day: Date, recipe: Recipe, servings: Int) {
        self.day = Calendar.current.startOfDay(for: day)
        self.recipe = recipe
        self.servings = max(1, servings)
        self.createdAt = .now
    }
}
