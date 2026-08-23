import Foundation
import SwiftData

/// Позиция каталога: что это за продукт и сколько в нём КБЖУ на 100 г.
/// Остаток в холодильнике живёт отдельно, в `StockItem`.
@Model
final class Product {
    /// Имя уникально в каталоге — по нему сходятся рецепты, остатки и журнал.
    var name: String = ""
    var category: String = ""
    /// «г», «мл» или «шт».
    var unit: String = "г"
    /// Сколько граммов в одной единице. Для «г» и «мл» — 1, для яйца — 55.
    var gramsPerUnit: Double = 1
    var caloriesPer100: Double = 0
    var proteinPer100: Double = 0
    var fatPer100: Double = 0
    var carbsPer100: Double = 0
    /// Ниже этого остатка продукт считается «на исходе» и едет в список покупок.
    var lowStockThreshold: Double = 0
    /// Срок годности в днях от даты покупки, если он вообще есть.
    var shelfLifeDays: Int?
    /// Фаза 2: заполняется сканером штрихкода.
    var barcode: String?
    var isCustom: Bool = false

    init(name: String, category: String, unit: String, gramsPerUnit: Double = 1,
         caloriesPer100: Double, proteinPer100: Double, fatPer100: Double,
         carbsPer100: Double, lowStockThreshold: Double = 0,
         shelfLifeDays: Int? = nil, barcode: String? = nil, isCustom: Bool = false) {
        self.name = name
        self.category = category
        self.unit = unit
        self.gramsPerUnit = gramsPerUnit
        self.caloriesPer100 = caloriesPer100
        self.proteinPer100 = proteinPer100
        self.fatPer100 = fatPer100
        self.carbsPer100 = carbsPer100
        self.lowStockThreshold = lowStockThreshold
        self.shelfLifeDays = shelfLifeDays
        self.barcode = barcode
        self.isCustom = isCustom
    }

    var nutritionPer100: Nutrition {
        Nutrition(calories: caloriesPer100, protein: proteinPer100,
                  fat: fatPer100, carbs: carbsPer100)
    }

    /// КБЖУ для количества, выраженного в единице продукта (шт, г или мл).
    func nutrition(forAmount amount: Double) -> Nutrition {
        Nutrition.from(per100: nutritionPer100, grams: amount * gramsPerUnit)
    }

    /// «3 шт (165 г)» — для штучных продуктов показываем и массу.
    func amountLabel(_ amount: Double) -> String {
        let base = Fmt.amount(amount, unit: unit)
        guard unit == "шт", gramsPerUnit != 1 else { return base }
        return "\(base) (\(Fmt.grams(amount * gramsPerUnit)))"
    }
}
