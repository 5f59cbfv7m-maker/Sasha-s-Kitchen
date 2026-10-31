import Foundation

/// Сколько чего лежит в холодильнике, в отрыве от SwiftData. Ключ — имя
/// продукта: оно уникально в каталоге, и на нём сходятся рецепты и остатки.
nonisolated struct StockSnapshot: Sendable {
    private let amounts: [String: Double]

    init(_ amounts: [String: Double]) { self.amounts = amounts }

    func available(_ productName: String) -> Double { amounts[productName] ?? 0 }
}

/// Сколько продукта нужно рецепту на выбранное число порций.
nonisolated struct IngredientRequirement: Sendable, Identifiable, Equatable {
    let productName: String
    let amount: Double
    let unit: String
    var id: String { productName }
}

/// Чего не хватает и сколько.
nonisolated struct Shortage: Sendable, Identifiable, Equatable {
    let productName: String
    let required: Double
    let available: Double
    let unit: String

    var id: String { productName }
    var missing: Double { max(0, required - available) }
}

nonisolated enum RecipeAvailability {

    enum Status: Equatable {
        case ready
        case missing([Shortage])

        var isReady: Bool { self == .ready }
    }

    /// Рецепт доступен, только если хватает каждого ингредиента без исключений —
    /// соль и масло здесь такие же участники, как мясо.
    static func status(for requirements: [IngredientRequirement],
                       stock: StockSnapshot) -> Status {
        let shortages = requirements.compactMap { requirement -> Shortage? in
            let have = stock.available(requirement.productName)
            // Допуск в сотую единицы: иначе 0.30000000000000004 г «не хватает».
            guard have + 0.01 < requirement.amount else { return nil }
            return Shortage(productName: requirement.productName,
                            required: requirement.amount,
                            available: have,
                            unit: requirement.unit)
        }
        return shortages.isEmpty ? .ready : .missing(shortages)
    }
}
