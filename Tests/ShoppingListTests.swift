import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Свой список покупок и синхронизация")
@MainActor
struct ShoppingListTests {

    @Test("Имя из каталога привязывает позицию к продукту, повтор не плодит строки")
    func addLinksToCatalog() throws {
        let context = try makeContext(seeded: true)
        let item = try #require(Kitchen.addToList("  молоко 2.5% ", amount: 500, in: context))
        #expect(item.product?.name == "Молоко 2.5%")
        Kitchen.addToList("МОЛОКО 2.5%", amount: 250, in: context)
        let all = try context.fetch(FetchDescriptor<ShoppingItem>())
        #expect(all.count == 1)
        #expect(all[0].amount == 750)
        #expect(Kitchen.addToList("   ", in: context) == nil)
        #expect(Kitchen.addToList("Батарейки", in: context)?.product == nil)
    }

    @Test("«Купил» пополняет остаток, снятая отметка возвращает как было")
    func boughtReplenishesAndUndoes() throws {
        let context = try makeContext(seeded: true)
        let milk = try stock("Молоко 2.5%", in: context)
        let before = milk.currentAmount
        let item = try #require(Kitchen.addToList("Молоко 2.5%", amount: 900, in: context))
        Kitchen.setBought(item, true, in: context)
        #expect(milk.currentAmount == before + 900)
        Kitchen.setBought(item, true, in: context)   // повторное нажатие не удваивает
        #expect(milk.currentAmount == before + 900)
        Kitchen.setBought(item, false, in: context)
        #expect(milk.currentAmount == before)
    }

    @Test("Недостающее для рецепта ложится в список с округлением вверх")
    func addMissing() throws {
        let context = try makeContext(seeded: true)
        try stock("Яйца куриные", in: context).currentAmount = 0.5
        let omelette = try recipe("Омлет классический", in: context)
        guard case .missing(let shortages) = Kitchen.status(
            for: omelette, servings: 2, stock: Kitchen.snapshot(in: context)) else {
            Issue.record("омлету должно не хватать яиц"); return
        }
        Kitchen.addMissing(shortages, in: context)
        let eggs = try #require(try context.fetch(FetchDescriptor<ShoppingItem>())
            .first { $0.name == "Яйца куриные" })
        #expect(eggs.amount == eggs.amount.rounded())
        #expect(eggs.amount >= 1)
    }

    @Test("Ингредиент удалённого продукта не держит рецепт «неготовым»")
    func deletedProductIsIgnored() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        let before = Kitchen.requirements(for: omelette, servings: 2).count
        context.delete(try product("Соль", in: context))
        try context.save()
        let after = Kitchen.requirements(for: omelette, servings: 2)
        #expect(after.count == before - 1)
        #expect(!after.contains { $0.productName == "—" })
    }

    @Test("Дубликаты после синхронизации сливаются в один продукт")
    func syncCleanupMergesDuplicates() throws {
        let context = try makeContext(seeded: true)
        // Второе устройство тоже загрузило стартовый набор.
        SeedLoader.load(into: context, localized: false, set: .russia)
        try context.save()
        #expect(try context.fetchCount(FetchDescriptor<Product>()) == 98)

        SyncCleanup.run(in: context)
        let products = try context.fetch(FetchDescriptor<Product>())
        #expect(products.count == 49)
        #expect(Set(products.map(\.name)).count == 49)
        #expect(try context.fetchCount(FetchDescriptor<Recipe>()) == 40)
        // На продукт — по-прежнему один остаток, и рецепты указывают на живые продукты.
        for product in products { #expect(product.stockItems?.count == 1, "\(product.name)") }
        for recipe in try context.fetch(FetchDescriptor<Recipe>()) {
            #expect(recipe.orderedIngredients.allSatisfy { $0.product != nil }, "\(recipe.name)")
        }
    }

    @Test("Срок годности считается по календарным дням")
    func expiryDays() throws {
        let product = Product(name: "Тест", category: "Молочка", unit: "г",
                              caloriesPer100: 1, proteinPer100: 1, fatPer100: 1, carbsPer100: 1)
        let tomorrowMorning = Calendar.current.date(
            byAdding: .day, value: 1, to: Calendar.current.startOfDay(for: .now))!
        let item = StockItem(product: product, currentAmount: 1, expiryDate: tomorrowMorning)
        #expect(item.daysUntilExpiry == 1)
    }
}
