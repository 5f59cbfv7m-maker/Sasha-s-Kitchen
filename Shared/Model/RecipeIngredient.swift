import Foundation
import SwiftData

/// Сколько продукта уходит на `Recipe.baseServings` порций.
@Model
final class RecipeIngredient {
    var product: Product?
    var amountPerBaseServing: Double = 0
    var order: Int = 0
    var recipe: Recipe?

    init(product: Product, amountPerBaseServing: Double, order: Int = 0) {
        self.product = product
        self.amountPerBaseServing = amountPerBaseServing
        self.order = order
    }

    var productName: String { product?.name ?? "—" }
    var unit: String { product?.unit ?? "г" }

    func amount(scaledBy factor: Double) -> Double {
        amountPerBaseServing * factor
    }

    func nutrition(scaledBy factor: Double) -> Nutrition {
        product?.nutrition(forAmount: amount(scaledBy: factor)) ?? .zero
    }
}
