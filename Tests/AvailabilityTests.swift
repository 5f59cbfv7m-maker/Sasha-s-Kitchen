import Foundation
import Testing
@testable import FridgeOracle

@Suite("Могу приготовить сейчас")
struct AvailabilityTests {

    private func requirement(_ name: String, _ amount: Double,
                             _ unit: String = "г") -> IngredientRequirement {
        IngredientRequirement(productName: name, amount: amount, unit: unit)
    }

    @Test("Хватает всего — рецепт доступен")
    func ready() {
        let stock = StockSnapshot(["Рис (сухой)": 500, "Соль": 100])
        let status = RecipeAvailability.status(
            for: [requirement("Рис (сухой)", 150), requirement("Соль", 3)], stock: stock)
        #expect(status == .ready)
    }

    @Test("Нехватка перечисляется поимённо и с количеством")
    func missingListed() {
        let stock = StockSnapshot(["Рис (сухой)": 100, "Соль": 0])
        let status = RecipeAvailability.status(
            for: [requirement("Рис (сухой)", 150), requirement("Соль", 3)], stock: stock)
        guard case .missing(let shortages) = status else {
            Issue.record("ожидалась нехватка")
            return
        }
        #expect(shortages.count == 2)
        #expect(shortages.first { $0.productName == "Рис (сухой)" }?.missing == 50)
        #expect(shortages.first { $0.productName == "Соль" }?.missing == 3)
    }

    @Test("Продукта нет в холодильнике вовсе — это тоже нехватка")
    func absentProduct() {
        let status = RecipeAvailability.status(
            for: [requirement("Форель охлаждённая", 300)], stock: StockSnapshot([:]))
        #expect(!status.isReady)
    }

    @Test("Ровно столько, сколько нужно — хватает")
    func exactAmount() {
        let status = RecipeAvailability.status(
            for: [requirement("Молоко 2.5%", 50, "мл")],
            stock: StockSnapshot(["Молоко 2.5%": 50]))
        #expect(status == .ready)
    }

    @Test("Погрешность двоичной арифметики не делает рецепт недоступным")
    func floatingPointTolerance() {
        let needed = 0.1 + 0.2   // 0.30000000000000004
        let status = RecipeAvailability.status(
            for: [requirement("Лимон", needed, "шт")],
            stock: StockSnapshot(["Лимон": 0.3]))
        #expect(status == .ready)
    }

    @Test("Больше порций — больше требования, доступность может пропасть")
    func servingsAffectAvailability() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        try stock("Яйца куриные", in: context).currentAmount = 3

        let snapshot = Kitchen.snapshot(in: context)
        #expect(Kitchen.status(for: omelette, servings: 2, stock: snapshot).isReady)
        #expect(!Kitchen.status(for: omelette, servings: 4, stock: snapshot).isReady)
    }
}
