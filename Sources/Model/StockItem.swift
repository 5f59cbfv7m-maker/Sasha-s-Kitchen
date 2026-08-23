import Foundation
import SwiftData

/// Остаток одного продукта в холодильнике. На продукт приходится ровно одна
/// запись: пополнение обновляет её, а не заводит вторую (см. `Kitchen.replenish`).
@Model
final class StockItem {
    var product: Product?
    var currentAmount: Double = 0
    /// Сколько было в момент последней покупки — от него считается прогресс-бар.
    var initialAmount: Double = 0
    var purchaseDate: Date = Date.now
    var expiryDate: Date?

    init(product: Product, currentAmount: Double, initialAmount: Double? = nil,
         purchaseDate: Date = .now, expiryDate: Date? = nil) {
        self.product = product
        self.currentAmount = currentAmount
        self.initialAmount = max(initialAmount ?? currentAmount, currentAmount)
        self.purchaseDate = purchaseDate
        self.expiryDate = expiryDate
    }

    var name: String { product?.name ?? "—" }
    var unit: String { product?.unit ?? "г" }
    var category: String { product?.category ?? "Прочее" }

    var isEmpty: Bool { currentAmount <= 0.0001 }
    var isLow: Bool { !isEmpty && currentAmount <= (product?.lowStockThreshold ?? 0) }

    /// 0…1 от изначального объёма. Пустой знаменатель не должен ронять вёрстку.
    var progress: Double {
        guard initialAmount > 0 else { return isEmpty ? 0 : 1 }
        return min(1, max(0, currentAmount / initialAmount))
    }

    var daysUntilExpiry: Int? {
        guard let expiryDate else { return nil }
        let days = Calendar.current.dateComponents([.day], from: .now, to: expiryDate).day
        return days
    }

    var isExpiringSoon: Bool {
        guard let days = daysUntilExpiry else { return false }
        return days <= 2
    }

    var nutrition: Nutrition {
        product?.nutrition(forAmount: currentAmount) ?? .zero
    }
}
