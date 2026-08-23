import Foundation

/// КБЖУ как значение. Чистый тип: никакого SwiftData и SwiftUI, поэтому его
/// можно складывать, масштабировать и проверять тестами в отрыве от базы.
nonisolated struct Nutrition: Equatable, Sendable {
    var calories: Double = 0
    var protein: Double = 0
    var fat: Double = 0
    var carbs: Double = 0

    static let zero = Nutrition()

    static func + (lhs: Nutrition, rhs: Nutrition) -> Nutrition {
        Nutrition(calories: lhs.calories + rhs.calories,
                  protein: lhs.protein + rhs.protein,
                  fat: lhs.fat + rhs.fat,
                  carbs: lhs.carbs + rhs.carbs)
    }

    static func += (lhs: inout Nutrition, rhs: Nutrition) { lhs = lhs + rhs }

    func scaled(by factor: Double) -> Nutrition {
        Nutrition(calories: calories * factor, protein: protein * factor,
                  fat: fat * factor, carbs: carbs * factor)
    }

    /// КБЖУ на 100 г, применённые к массе `grams`.
    static func from(per100: Nutrition, grams: Double) -> Nutrition {
        per100.scaled(by: grams / 100)
    }
}

nonisolated extension Sequence where Element == Nutrition {
    var total: Nutrition { reduce(.zero, +) }
}
