import Foundation
import SwiftData

@Model
final class Recipe {
    var name: String = ""
    /// Фото пользовательского рецепта. Крупные блобы SwiftData хранит рядом с базой.
    @Attribute(.externalStorage) var photoData: Data?
    var cookingTimeMinutes: Int = 0
    var baseServings: Int = 2
    var steps: String?
    var isCustom: Bool = false
    var createdAt: Date = Date.now

    @Relationship(deleteRule: .cascade, inverse: \RecipeIngredient.recipe)
    var ingredients: [RecipeIngredient] = []

    init(name: String, cookingTimeMinutes: Int, baseServings: Int,
         steps: String? = nil, isCustom: Bool = false, photoData: Data? = nil) {
        self.name = name
        self.cookingTimeMinutes = cookingTimeMinutes
        self.baseServings = max(1, baseServings)
        self.steps = steps
        self.isCustom = isCustom
        self.photoData = photoData
        self.createdAt = .now
    }

    /// SwiftData не гарантирует порядок в связи «ко многим» — держим его сами.
    var orderedIngredients: [RecipeIngredient] {
        ingredients.sorted { ($0.order, $0.productName) < ($1.order, $1.productName) }
    }

    /// Во сколько раз пересчитать граммовки под нужное число порций.
    func scale(for servings: Int) -> Double {
        Double(max(1, servings)) / Double(max(1, baseServings))
    }

    /// КБЖУ всего блюда на `servings` порций.
    func nutrition(for servings: Int) -> Nutrition {
        let factor = scale(for: servings)
        return ingredients.map { $0.nutrition(scaledBy: factor) }.total
    }

    func nutritionPerServing(for servings: Int) -> Nutrition {
        nutrition(for: servings).scaled(by: 1 / Double(max(1, servings)))
    }
}
