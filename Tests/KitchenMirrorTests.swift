import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Семейная кухня: перевод кухни в записи и обратно")
@MainActor
struct KitchenMirrorTests {

    private func sorted(_ records: [MirrorRecord]) -> [MirrorRecord] {
        records.sorted { $0.recordName < $1.recordName }
    }

    @Test("Вся кухня переезжает в пустую базу без потерь: та же выгрузка до поля")
    func roundTrip() throws {
        let owner = try makeContext(seeded: true)
        SyncCleanup.run(in: owner)
        Kitchen.addToList("Батарейки", amount: 4, in: owner)
        let omelette = try recipe("Омлет классический", in: owner)
        MealPlan.add(omelette, on: .now, servings: 3, in: owner)
        let amounts = Dictionary(Kitchen.requirements(for: omelette, servings: 2).map { ($0.productName, $0.amount) },
                                 uniquingKeysWith: +)
        Kitchen.cook(recipe: omelette, servings: 2, amounts: amounts, in: owner)
        let exported = KitchenMirror.export(owner, access: .everything)
        #expect(Set(exported.map(\.type)) == Set(KitchenMirror.types(for: .everything)))

        let guest = try makeContext()
        KitchenMirror.apply(exported.shuffled(), to: guest)
        let copied = KitchenMirror.export(guest, access: .everything)
        let original = Dictionary(exported.map { ($0.recordName, $0) }, uniquingKeysWith: { a, _ in a })
        let copy = Dictionary(copied.map { ($0.recordName, $0) }, uniquingKeysWith: { a, _ in a })
        #expect(Set(copy.keys).subtracting(original.keys).isEmpty, "лишние: \(Set(copy.keys).subtracting(original.keys))")
        #expect(Set(original.keys).subtracting(copy.keys).isEmpty, "потеряны: \(Set(original.keys).subtracting(copy.keys))")
        for (name, record) in original where copy[name] != nil && copy[name] != record {
            Issue.record("\(name) отличается")
        }

        // Повторное внесение ничего не плодит.
        KitchenMirror.apply(exported, to: guest)
        #expect(try guest.fetchCount(FetchDescriptor<Product>()) == owner.fetchCount(FetchDescriptor<Product>()))
    }

    @Test("Доступ «только холодильник» — только продукты и остатки")
    func fridgeOnly() throws {
        let context = try makeContext(seeded: true)
        SyncCleanup.run(in: context)
        let records = KitchenMirror.export(context, access: .fridge)
        #expect(Set(records.map(\.type)) == ["Product", "StockItem"])
    }

    @Test("Отправляется только изменённое; исчезнувшее — удаляется; удаление доходит до базы")
    func diffAndDelete() throws {
        let context = try makeContext(seeded: true)
        SyncCleanup.run(in: context)
        let first = KitchenMirror.export(context, access: .fridge)
        let sent = Dictionary(first.map { ($0.recordName, $0) }, uniquingKeysWith: { a, _ in a })
        #expect(KitchenMirror.diff(current: first, sent: sent).save.isEmpty)

        let milk = try stock("Молоко 2.5%", in: context)
        milk.currentAmount = 999
        let plan = KitchenMirror.diff(current: KitchenMirror.export(context, access: .fridge), sent: sent)
        #expect(plan.save.map(\.uid) == [milk.uid])
        #expect(plan.delete.isEmpty)

        let guest = try makeContext()
        KitchenMirror.apply(first, to: guest)
        KitchenMirror.apply([], deleted: ["StockItem-\(milk.uid)"], to: guest)
        #expect(try guest.fetch(FetchDescriptor<StockItem>()).contains { $0.uid == milk.uid } == false)
    }
}
