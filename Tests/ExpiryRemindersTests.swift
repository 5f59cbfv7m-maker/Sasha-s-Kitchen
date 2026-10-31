import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Напоминания о сроках годности")
@MainActor
struct ExpiryRemindersTests {

    private let calendar = Calendar(identifier: .gregorian)

    private func day(_ offset: Int, from now: Date) -> Date {
        calendar.date(byAdding: .day, value: offset, to: calendar.startOfDay(for: now))!
    }

    @Test("Одно уведомление на день, накануне в выбранное время; пустое и прошедшее — нет")
    func planGroupsByDay() throws {
        let context = try makeContext(seeded: true)
        let now = calendar.date(bySettingHour: 12, minute: 0, second: 0, of: .now)!
        for item in Kitchen.allStock(in: context) { item.expiryDate = nil }
        let milk = try stock("Молоко 2.5%", in: context), cheese = try stock("Творог 5%", in: context)
        let eggs = try stock("Яйца куриные", in: context), rice = try stock("Рис (сухой)", in: context)
        milk.expiryDate = day(3, from: now)
        cheese.expiryDate = day(3, from: now).addingTimeInterval(3600 * 20)
        eggs.expiryDate = day(1, from: now)      // накануне — сегодня в 10:00, уже прошло
        rice.expiryDate = day(5, from: now)
        rice.currentAmount = 0                   // пустое не напоминаем

        let plan = ExpiryReminders.plan(stock: Kitchen.allStock(in: context), recipes: [], minutes: 600,
                                        now: now, calendar: calendar)
        #expect(plan.count == 1)
        #expect(plan.first?.products == ["Молоко 2.5%", "Творог 5%"])
        #expect(plan.first?.fireDate == calendar.date(byAdding: .minute, value: 600, to: day(2, from: now)))
    }

    @Test("К уведомлению подбирается готовый рецепт из того, что испортится")
    func suggestsReadyRecipe() throws {
        let context = try makeContext(seeded: true)
        let recipes = try context.fetch(FetchDescriptor<Recipe>())
        let snapshot = Kitchen.snapshot(in: context)
        let name = ExpiryReminders.bestRecipe(using: ["Яйца куриные", "Молоко 2.5%"], from: recipes, stock: snapshot)
        let recipe = try #require(recipes.first { $0.name == name })
        #expect(Kitchen.status(for: recipe, servings: recipe.baseServings, stock: snapshot).isReady)
        #expect(recipe.orderedIngredients.contains { ["Яйца куриные", "Молоко 2.5%"].contains($0.productName) })
        #expect(ExpiryReminders.bestRecipe(using: ["Нет такого"], from: recipes, stock: snapshot) == nil)
    }
}
