import CloudKit
import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Общий список через iCloud")
@MainActor
struct SharedListTests {

    private func want(_ name: String, _ amount: Double = 0, _ kind: Purchase.Kind = .listed) -> SharedListPlan.Wanted {
        .init(key: SharedListPlan.key(for: name), title: name, amount: amount, unit: "", kind: kind)
    }

    private func purchase(_ name: String) -> Purchase {
        Purchase(zoneOwner: CKCurrentUserDefaultName, key: SharedListPlan.key(for: name), title: name)
    }

    @Test("Ключ записи не зависит от регистра, диакритики и пробелов и годится в имя записи")
    func keyIsStable() {
        let key = SharedListPlan.key(for: "Молоко")
        #expect(SharedListPlan.key(for: "  МОЛОКО ") == key)
        #expect(SharedListPlan.key(for: "Café") == SharedListPlan.key(for: "cafe"))
        #expect(key != SharedListPlan.key(for: "Кефир"))
        #expect(key.allSatisfy(\.isASCII) && key.count < 64)
    }

    @Test("Зона получает недостающее, обновляет изменённое и убирает лишнее")
    func reconcileSavesAndDeletes() {
        var milk = purchase("Молоко")
        milk.amount = 500
        let plan = SharedListPlan.reconcile(
            wanted: [want("Молоко", 900), want("Хлеб"), want("хлеб")],
            current: [milk, purchase("Сыр")])
        #expect(plan.save.map(\.title).sorted() == ["Молоко", "Хлеб"])
        #expect(plan.save.first { $0.title == "Молоко" }?.amount == 900)
        #expect(plan.delete.map(\.title) == ["Сыр"])

        let again = SharedListPlan.reconcile(wanted: [want("Молоко", 900)], current: plan.save)
        #expect(again.save.isEmpty)
    }

    @Test("Пока гость ждёт ответа владельца, его записи не трогаются")
    func reconcileKeepsPendingGuestWork() {
        var added = purchase("Батарейки")
        added.byGuest = true
        var ticked = purchase("Сыр")
        ticked.isBought = true
        let plan = SharedListPlan.reconcile(wanted: [want("Батарейки")], current: [added, ticked])
        #expect(plan.save.isEmpty)
        #expect(plan.delete.isEmpty)

        let busy = SharedListPlan.reconcile(wanted: [], current: [purchase("Кефир")],
                                            busy: [SharedListPlan.key(for: "Кефир")])
        #expect(busy.delete.isEmpty)
    }

    @Test("Купленное, которое снова нужно, возвращается неотмеченным")
    func reconcileResetsAppliedPurchase() {
        var milk = purchase("Молоко")
        milk.isBought = true
        milk.applied = true
        let plan = SharedListPlan.reconcile(wanted: [want("Молоко", 1, .out)], current: [milk])
        let saved = plan.save.first
        #expect(saved?.isBought == false && saved?.applied == false && saved?.kind == .out)
    }

    @Test("Заявки: отметка гостя — «купить», новая позиция — «добавить», проведённое — ничего")
    func claims() {
        var bought = purchase("Молоко")
        bought.isBought = true
        var added = purchase("Хлеб")
        added.byGuest = true
        var done = purchase("Сыр")
        done.isBought = true
        done.applied = true
        let claims = SharedListPlan.claims(in: [bought, added, done, purchase("Кефир")])
        #expect(claims.map(\.0) == [.buy, .add])
        #expect(claims[0].1.applied && claims[1].1.imported)
    }

    @Test("Отметка гостя пополняет холодильник, его позиция ложится в список один раз")
    func applyOnOwnerDevice() throws {
        let context = try makeContext(seeded: true)
        let milk = try stock("Молоко 2.5%", in: context)
        milk.currentAmount = 0
        try context.save()

        var bought = purchase("Молоко 2.5%")
        bought.isBought = true
        bought.amount = 1000
        SharedLists.apply(.buy, bought, in: context)
        #expect(milk.currentAmount == 1000)

        var added = purchase("Батарейки")
        added.byGuest = true
        SharedLists.apply(.add, added, in: context)
        SharedLists.apply(.add, added, in: context)
        let listed = try context.fetch(FetchDescriptor<ShoppingItem>())
        #expect(listed.map(\.name) == ["Батарейки"])

        var ticked = added
        ticked.isBought = true
        SharedLists.apply(.buy, ticked, in: context)
        #expect(listed[0].isBought)
    }

    @Test("Желаемое содержимое зоны — свой список и то, что кончается в холодильнике")
    func wantedMirrorsList() throws {
        let context = try makeContext(seeded: true)
        try stock("Молоко 2.5%", in: context).currentAmount = 0
        Kitchen.addToList("Батарейки", amount: 4, in: context)
        let wanted = SharedLists.wanted(in: context)
        #expect(wanted.first?.title == "Батарейки")
        #expect(wanted.first?.kind == .listed)
        #expect(wanted.contains { $0.title == "Молоко 2.5%" && $0.kind == .out && $0.amount > 0 })
    }
}
