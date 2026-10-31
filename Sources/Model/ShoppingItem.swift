import Foundation
import SwiftData

/// Позиция, которую вписали в список покупок руками: «батарейки», «хлеб» или
/// недостающее для рецепта. Остатки холодильника в список попадают сами и
/// здесь не хранятся.
@Model
final class ShoppingItem {
    var name: String = ""
    /// Сколько взять, в единице продукта; 0 — не указано.
    var amount: Double = 0
    /// Если позиция — продукт из каталога, «Купил» пополняет его остаток.
    var product: Product?
    var isBought: Bool = false
    var createdAt: Date = Date.now

    init(name: String, amount: Double = 0, product: Product? = nil) {
        self.name = name
        self.amount = amount
        self.product = product
        self.createdAt = .now
    }

    var unit: String { product?.unit ?? "" }
}
