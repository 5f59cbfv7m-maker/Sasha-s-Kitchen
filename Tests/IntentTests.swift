import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Siri и Команды")
@MainActor
struct IntentTests {

    @Test("«Что купить» — своё вписанное и то, что кончается, без повторов")
    func shoppingListNames() throws {
        let context = try makeContext(seeded: true)
        try stock("Молоко 2.5%", in: context).currentAmount = 0
        Kitchen.addToList("Батарейки", in: context)
        Kitchen.addToList("молоко 2.5%", in: context)
        let names = ShoppingListIntent.names(in: context)
        #expect(names.first == "Батарейки")
        #expect(names.filter { $0.sameName(as: "Молоко 2.5%") }.count == 1)
        #expect(names.count == Kitchen.shoppingEntries(in: context).count + 1)
    }

    @Test("«Купил» по имени: вписанное отмечается, кончившееся пополняется")
    func markBought() throws {
        let context = try makeContext(seeded: true)
        let milk = try stock("Молоко 2.5%", in: context)
        milk.currentAmount = 0
        MarkBoughtIntent.markBought("МОЛОКО 2.5%", in: context)
        #expect(milk.currentAmount > 0)

        let item = try #require(Kitchen.addToList("Батарейки", in: context))
        MarkBoughtIntent.markBought("батарейки", in: context)
        #expect(item.isBought)
    }
}
