import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Меню на неделю")
@MainActor
struct MealPlanTests {

    @Test("Нехватка считается по всему меню: молоко для двух блюд складывается")
    func shortagesAddUp() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        let milk = try stock("Молоко 2.5%", in: context)
        let need = Kitchen.requirements(for: omelette, servings: 2).first { $0.productName == "Молоко 2.5%" }!.amount
        milk.currentAmount = need * 1.5   // на один омлет хватает, на два — нет
        MealPlan.add(omelette, on: .now, servings: 2, in: context)
        let snapshot = Kitchen.snapshot(in: context)
        #expect(!MealPlan.shortages(for: MealPlan.upcoming(in: context), stock: snapshot)
            .contains { $0.productName == "Молоко 2.5%" })

        MealPlan.add(omelette, on: Calendar.current.date(byAdding: .day, value: 1, to: .now)!, servings: 2, in: context)
        let milkShortage = try #require(MealPlan.shortages(for: MealPlan.upcoming(in: context), stock: snapshot)
            .first { $0.productName == "Молоко 2.5%" })
        #expect(abs(milkShortage.missing - need * 0.5) < 0.001)
    }

    @Test("Приготовили — блюдо в меню отмечено; удалили рецепт — ушло из меню")
    func cookingMarksMeal() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        MealPlan.add(omelette, on: .now, servings: 2, in: context)
        let amounts = Dictionary(Kitchen.requirements(for: omelette, servings: 2).map { ($0.productName, $0.amount) },
                                 uniquingKeysWith: +)
        Kitchen.cook(recipe: omelette, servings: 2, amounts: amounts, in: context)
        let meals = try context.fetch(FetchDescriptor<PlannedMeal>())
        #expect(meals.count == 1 && meals[0].isCooked)
        #expect(MealPlan.upcoming(in: context).isEmpty)

        context.delete(omelette)
        try context.save()
        #expect(try context.fetchCount(FetchDescriptor<PlannedMeal>()) == 0)
    }
}
