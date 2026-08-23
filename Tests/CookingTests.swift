import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Готовка списывает остатки")
struct CookingTests {

    @Test("Фактические граммовки уходят из холодильника")
    func deductsStock() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        let eggs = try stock("Яйца куриные", in: context)
        let butter = try stock("Сливочное масло", in: context)
        eggs.currentAmount = 10
        butter.currentAmount = 200

        let amounts = Dictionary(uniqueKeysWithValues:
            Kitchen.requirements(for: omelette, servings: 2).map { ($0.productName, $0.amount) })
        Kitchen.cook(recipe: omelette, servings: 2, amounts: amounts, in: context)

        #expect(eggs.currentAmount == 7)      // 10 − 3 шт
        #expect(butter.currentAmount == 190)  // 200 − 10 г
    }

    @Test("Правка фактического веса меняет и списание, и журнал")
    func actualAmountsWin() throws {
        let context = try makeContext(seeded: true)
        let fried = try recipe("Жареная картошка с луком", in: context)
        let potato = try stock("Картофель", in: context)
        potato.currentAmount = 1_000

        // Расчёт просит 400 г, но в сковородку ушло 550.
        Kitchen.cook(recipe: fried, servings: 2,
                     amounts: ["Картофель": 550, "Лук репчатый": 80,
                               "Растительное масло": 40, "Соль": 4],
                     in: context)

        #expect(potato.currentAmount == 450)
        let log = try #require(try context.fetch(FetchDescriptor<CookingLog>()).first)
        #expect(log.actualAmountsUsed["Картофель"] == 550)
    }

    @Test("Холодильник не уходит в минус")
    func neverGoesNegative() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        let eggs = try stock("Яйца куриные", in: context)
        eggs.currentAmount = 1

        Kitchen.cook(recipe: omelette, servings: 2,
                     amounts: ["Яйца куриные": 3], in: context)

        #expect(eggs.currentAmount == 0)
        let log = try #require(try context.fetch(FetchDescriptor<CookingLog>()).first)
        // В журнал попадает списанное, а не запрошенное.
        #expect(log.actualAmountsUsed["Яйца куриные"] == 1)
    }

    @Test("КБЖУ журнала считается по фактически списанному")
    func logNutritionMatchesDeduction() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        try stock("Яйца куриные", in: context).currentAmount = 10
        try stock("Молоко 2.5%", in: context).currentAmount = 1_000
        try stock("Сливочное масло", in: context).currentAmount = 200
        try stock("Соль", in: context).currentAmount = 500

        Kitchen.cook(recipe: omelette, servings: 2,
                     amounts: ["Яйца куриные": 3, "Молоко 2.5%": 50,
                               "Сливочное масло": 10, "Соль": 2],
                     in: context)

        let log = try #require(try context.fetch(FetchDescriptor<CookingLog>()).first)
        // 165 г яйца + 50 мл молока + 10 г масла.
        let expected = 165 * 1.55 + 50 * 0.52 + 10 * 7.48
        #expect(abs(log.calories - expected) < 0.01)
        #expect(abs(log.nutritionPerServing.calories - expected / 2) < 0.01)
        #expect(log.recipeName == "Омлет классический")
        #expect(log.servings == 2)
    }

    @Test("После готовки продукт может попасть в список покупок")
    func cookingFeedsShoppingList() throws {
        let context = try makeContext(seeded: true)
        let grill = try recipe("Куриная грудка гриль с лимоном", in: context)
        let chicken = try stock("Куриное филе", in: context)
        chicken.currentAmount = 360

        #expect(!Kitchen.shoppingEntries(in: context).contains { $0.item.name == "Куриное филе" })

        let amounts = Dictionary(uniqueKeysWithValues:
            Kitchen.requirements(for: grill, servings: 2).map { ($0.productName, $0.amount) })
        Kitchen.cook(recipe: grill, servings: 2, amounts: amounts, in: context)

        #expect(chicken.currentAmount == 10)
        #expect(Kitchen.shoppingEntries(in: context).contains { $0.item.name == "Куриное филе" })
    }

    @Test("Пополнение поднимает остаток и обновляет срок годности")
    func replenish() throws {
        let context = try makeContext(seeded: true)
        let trout = try stock("Форель охлаждённая", in: context)
        #expect(trout.isEmpty)

        // Ровно то, что делает кнопка «Купил» на вкладке «Для покупки».
        let suggested = Kitchen.suggestedPurchase(for: trout)
        Kitchen.replenish(trout, by: suggested)

        #expect(trout.currentAmount == suggested)
        #expect(trout.progress > 0.99, "после покупки шкала должна быть полной")
        let days = try #require(trout.daysUntilExpiry)
        #expect(days >= 1)
    }
}
