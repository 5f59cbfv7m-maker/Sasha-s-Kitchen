import Foundation
import SwiftData

/// Что и когда приготовили. Имя рецепта дублируется строкой: журнал должен
/// пережить удаление пользовательского рецепта.
@Model
final class CookingLog {
    var recipeName: String = ""
    var recipe: Recipe?
    var date: Date = Date.now
    var servings: Int = 1
    /// Имя продукта → фактически списанное количество в единице этого продукта.
    var actualAmountsUsed: [String: Double] = [:]
    var calories: Double = 0
    var protein: Double = 0
    var fat: Double = 0
    var carbs: Double = 0

    init(recipe: Recipe?, recipeName: String, servings: Int,
         actualAmountsUsed: [String: Double], nutrition: Nutrition, date: Date = .now) {
        self.recipe = recipe
        self.recipeName = recipeName
        self.servings = servings
        self.actualAmountsUsed = actualAmountsUsed
        self.calories = nutrition.calories
        self.protein = nutrition.protein
        self.fat = nutrition.fat
        self.carbs = nutrition.carbs
        self.date = date
    }

    var nutrition: Nutrition {
        Nutrition(calories: calories, protein: protein, fat: fat, carbs: carbs)
    }

    var nutritionPerServing: Nutrition {
        nutrition.scaled(by: 1 / Double(max(1, servings)))
    }
}
