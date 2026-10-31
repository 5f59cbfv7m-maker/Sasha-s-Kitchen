import CloudKit
import CryptoKit
import Foundation
import Observation
import SwiftData
import SwiftUI

/// Позиция общего списка покупок — то, что лежит в записи CloudKit.
nonisolated struct Purchase: Codable, Sendable, Identifiable, Equatable {
    enum Kind: String, Codable, Sendable { case listed, out, low }

    /// Владелец зоны: `CKCurrentUserDefaultName` для своего списка, имя
    /// записи пользователя-владельца — для чужого.
    var zoneOwner: String
    /// Имя записи — производное от названия (`SharedListPlan.key`), поэтому
    /// «Молоко» с двух устройств и от гостя — одна и та же запись.
    var key: String
    var title: String
    /// Сколько взять, в единице `unit` (код «г»/«мл»/«шт»); 0 — не указано.
    var amount: Double = 0
    var unit: String = ""
    var kind: Kind = .listed
    /// Отметил гость.
    var isBought = false
    /// Вписал гость; `imported` — владелец перенёс позицию в свой список.
    var byGuest = false
    var imported = false
    /// Владелец уже провёл отметку гостя через свой холодильник.
    var applied = false
    /// Системные поля записи (тег изменения) — без них сервер не отличит
    /// правку свежей версии от правки устаревшей.
    var systemFields: Data?

    var id: String { zoneOwner + "/" + key }
    var isOwn: Bool { zoneOwner == CKCurrentUserDefaultName }
    /// Запись больше ничего не ждёт от владельца — её можно убрать.
    var isSettled: Bool { (!byGuest || imported) && (!isBought || applied) }

    func sameContent(as other: Purchase) -> Bool {
        var a = self, b = other
        a.systemFields = nil
        b.systemFields = nil
        return a == b
    }
}

/// Решения о записях общего списка — чистые функции, их проверяют тесты.
nonisolated enum SharedListPlan {

    struct Wanted: Equatable {
        var key: String
        var title: String
        var amount: Double
        var unit: String
        var kind: Purchase.Kind
    }

    /// Имя записи CloudKit из названия: регистр, диакритика и ширина не
    /// важны, как и в `String.sameName`. Хеш — потому что имя записи должно
    /// быть ASCII.
    static func key(for name: String) -> String {
        let folded = name.trimmingCharacters(in: .whitespacesAndNewlines)
            .folding(options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive], locale: nil)
        let digest = SHA256.hash(data: Data(folded.utf8))
        return "p-" + digest.prefix(16).map { String(format: "%02x", $0) }.joined()
    }

    /// Что сохранить и что удалить в своей зоне, чтобы в ней было ровно то,
    /// что нужно купить. `busy` — записи с неподтверждённой заявкой: их не
    /// трогаем, пока сервер не ответит.
    static func reconcile(wanted: [Wanted], current: [Purchase],
                          busy: Set<String> = []) -> (save: [Purchase], delete: [Purchase]) {
        let byKey = Dictionary(current.map { ($0.key, $0) }, uniquingKeysWith: { first, _ in first })
        var seen = busy
        var save: [Purchase] = []
        for want in wanted where seen.insert(want.key).inserted {
            let old = byKey[want.key]
            // Вписанное гостем сначала переносится в список владельца.
            if let old, old.byGuest, !old.imported { continue }
            var new = old ?? Purchase(zoneOwner: CKCurrentUserDefaultName, key: want.key, title: want.title)
            new.title = want.title
            new.amount = want.amount
            new.unit = want.unit
            new.kind = want.kind
            // Купленное снова понадобилось — снимаем отметку.
            if new.applied {
                new.isBought = false
                new.applied = false
            }
            if old.map({ !new.sameContent(as: $0) }) ?? true { save.append(new) }
        }
        let delete = current.filter { !seen.contains($0.key) && $0.isSettled }
        return (save, delete)
    }

    enum Claim: Equatable { case add, buy }

    /// Что гость сделал и владелец ещё не провёл: заявка и запись, которую
    /// надо отправить, чтобы её застолбить.
    static func claims(in current: [Purchase]) -> [(Claim, Purchase)] {
        current.compactMap { purchase in
            var claimed = purchase
            if purchase.isBought && !purchase.applied {
                claimed.applied = true
                claimed.imported = purchase.byGuest || purchase.imported
                return (.buy, claimed)
            }
            if purchase.byGuest && !purchase.imported {
                claimed.imported = true
                return (.add, claimed)
            }
            return nil
        }
    }
}

/// Общий список покупок через iCloud. Владелец делится зоной `Shopping`
/// своей приватной базы (CKShare на всю зону); у гостя она появляется в
/// общей базе, и он видит список у себя в приложении, отмечает купленное и
/// дописывает своё. SwiftData делиться не умеет, поэтому список зеркалится в
/// эту зону через CKSyncEngine, а сама SwiftData остаётся как была.
///
/// Каждое устройство владельца кладёт в зону то, что сейчас нужно купить
/// (`reconcile`). Отметку или добавление гостя проводит одно устройство —
/// то, чья заявка первой дошла до сервера: у остальных запись уже изменилась,
/// и сервер отвечает конфликтом. Иначе холодильник пополнился бы дважды.
@Observable
final class SharedLists {
    static let shared = SharedLists()
    nonisolated static let containerID = "iCloud.com.kirillrychkov.FridgeOracle"
    nonisolated static let zoneName = "Shopping"
    private static let recordType = "Purchase"

    private(set) var items: [String: Purchase] = [:]
    private(set) var shares: [String: CKShare] = [:]
    /// Запущен ли обмен: нужен вход в iCloud.
    private(set) var isAvailable = false

    @ObservationIgnored private(set) var container: CKContainer?
    @ObservationIgnored private(set) var privateEngine: CKSyncEngine?
    @ObservationIgnored private(set) var sharedEngine: CKSyncEngine?
    @ObservationIgnored private var context: ModelContext?
    @ObservationIgnored private var claims: [String: SharedListPlan.Claim] = [:]
    @ObservationIgnored private var states = States()
    /// Имена владельцев для демо-списка на скриншотах — у него нет CKShare.
    @ObservationIgnored private var demoNames: [String: String] = [:]

    private struct States: Codable {
        var privateDatabase: CKSyncEngine.State.Serialization?
        var sharedDatabase: CKSyncEngine.State.Serialization?
    }

    private struct Stored: Codable {
        var states: States
        var items: [Purchase]
        var shares: [String: Data]
    }

    var ownShare: CKShare? { shares[CKCurrentUserDefaultName] }

    func start(context: ModelContext) {
        guard privateEngine == nil, FileManager.default.ubiquityIdentityToken != nil else { return }
        self.context = context
        load()
        let container = CKContainer(identifier: Self.containerID)
        self.container = container
        privateEngine = CKSyncEngine(.init(database: container.privateCloudDatabase,
                                           stateSerialization: states.privateDatabase, delegate: self))
        sharedEngine = CKSyncEngine(.init(database: container.sharedCloudDatabase,
                                          stateSerialization: states.sharedDatabase, delegate: self))
        FamilyKitchen.shared.start(context: context)
        isAvailable = true
    }

    // MARK: Владелец

    /// Зона и CKShare на неё — для приглашения. Уже есть — отдаём её.
    static func makeShare() async throws -> CKShare {
        let lists = SharedLists.shared
        if let share = lists.ownShare { return share }
        guard let container = lists.container else { throw CKError(.notAuthenticated) }
        let database = container.privateCloudDatabase
        let zone = CKRecordZone(zoneName: zoneName)
        _ = try await database.modifyRecordZones(saving: [zone], deleting: [])
        let share = CKShare(recordZoneID: zone.zoneID)
        share[CKShare.SystemFieldKey.title] = String(localized: "Список покупок")
        share.publicPermission = .none
        let result = try await database.modifyRecords(saving: [share], deleting: [])
        guard let saved = try result.saveResults[share.recordID]?.get() as? CKShare else {
            throw CKError(.internalError)
        }
        lists.receive(saved)
        lists.reconcile()
        return saved
    }

    /// Владелец закрыл доступ: зона больше не нужна, гости её потеряют.
    func stopSharing() {
        let zoneID = CKRecordZone.ID(zoneName: Self.zoneName)
        privateEngine?.state.add(pendingDatabaseChanges: [.deleteZone(zoneID)])
        drop(owner: CKCurrentUserDefaultName)
    }

    /// Привести свою зону к текущему списку. Зовётся при каждом изменении
    /// холодильника и списка и после всего, что пришло с сервера.
    func reconcile() {
        guard let context, let engine = privateEngine, ownShare != nil else { return }
        let own = items.values.filter(\.isOwn)

        for (claim, purchase) in SharedListPlan.claims(in: own) where claims[purchase.id] == nil {
            claims[purchase.id] = claim
            items[purchase.id] = purchase
            engine.state.add(pendingRecordZoneChanges: [.saveRecord(recordID(for: purchase))])
        }

        let busy = Set(claims.keys.compactMap { items[$0]?.key })
        let plan = SharedListPlan.reconcile(wanted: Self.wanted(in: context),
                                            current: items.values.filter(\.isOwn), busy: busy)
        for purchase in plan.save { items[purchase.id] = purchase }
        for purchase in plan.delete { items[purchase.id] = nil }
        engine.state.add(pendingRecordZoneChanges:
            plan.save.map { .saveRecord(recordID(for: $0)) } + plan.delete.map { .deleteRecord(recordID(for: $0)) })
        persist()
    }

    static func wanted(in context: ModelContext) -> [SharedListPlan.Wanted] {
        let listed = (try? context.fetch(FetchDescriptor<ShoppingItem>(sortBy: [SortDescriptor(\.createdAt)]))) ?? []
        let mine = listed.filter { !$0.isBought }.map {
            SharedListPlan.Wanted(key: SharedListPlan.key(for: $0.name), title: $0.name,
                                  amount: $0.amount, unit: $0.unit, kind: .listed)
        }
        let fridge = Kitchen.shoppingEntries(in: context).map {
            SharedListPlan.Wanted(key: SharedListPlan.key(for: $0.item.name), title: $0.item.name,
                                  amount: $0.suggested, unit: $0.item.unit,
                                  kind: $0.state == .out ? .out : .low)
        }
        return mine + fridge
    }

    /// Провести у себя то, что сделал гость: отметку «купил» или новую позицию.
    static func apply(_ claim: SharedListPlan.Claim, _ purchase: Purchase, in context: ModelContext) {
        let listed = ((try? context.fetch(FetchDescriptor<ShoppingItem>())) ?? [])
            .first { !$0.isBought && SharedListPlan.key(for: $0.name) == purchase.key }
        let entry = Kitchen.shoppingEntries(in: context)
            .first { SharedListPlan.key(for: $0.item.name) == purchase.key }
        switch claim {
        case .buy:
            if let listed {
                Kitchen.setBought(listed, true, in: context)
            } else if let entry {
                Kitchen.replenish(entry.item, by: purchase.amount > 0 ? purchase.amount : entry.suggested)
                try? context.save()
            }
        case .add:
            // Уже есть в списке у владельца — второй строки не нужно.
            guard listed == nil, entry == nil else { return }
            Kitchen.addToList(purchase.title, amount: purchase.amount, in: context)
        }
    }

    // MARK: Гость

    struct GuestList: Identifiable {
        let owner: String
        let name: String
        let share: CKShare?
        let items: [Purchase]
        var id: String { owner }
    }

    var guestLists: [GuestList] {
        let owners = Set(items.values.map(\.zoneOwner)).union(shares.keys)
            .subtracting([CKCurrentUserDefaultName])
        return owners.map { owner in
            GuestList(owner: owner, name: name(of: owner), share: shares[owner],
                      items: items.values.filter { $0.zoneOwner == owner }.sorted(by: Self.order))
        }
        .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    var guestCount: Int {
        items.values.filter { !$0.isOwn && !$0.isBought }.count
    }

    func toggle(_ purchase: Purchase) {
        guard var purchase = items[purchase.id], !purchase.isOwn, !purchase.applied else { return }
        purchase.isBought.toggle()
        items[purchase.id] = purchase
        sharedEngine?.state.add(pendingRecordZoneChanges: [.saveRecord(recordID(for: purchase))])
        persist()
    }

    func add(_ name: String, toListOf owner: String) {
        let title = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty else { return }
        let purchase = Purchase(zoneOwner: owner, key: SharedListPlan.key(for: title), title: title, byGuest: true)
        guard items[purchase.id] == nil else { return }
        items[purchase.id] = purchase
        sharedEngine?.state.add(pendingRecordZoneChanges: [.saveRecord(recordID(for: purchase))])
        persist()
    }

    /// Чужой список без iCloud — только для скриншотов App Store (`-storeDemo`).
    func showDemo(owner name: String, items demo: [(title: String, amount: Double, unit: String, isBought: Bool)]) {
        let owner = "demo"
        demoNames[owner] = name
        for item in demo {
            let purchase = Purchase(zoneOwner: owner, key: SharedListPlan.key(for: item.title), title: item.title,
                                    amount: item.amount, unit: item.unit, isBought: item.isBought)
            items[purchase.id] = purchase
        }
    }

    /// Гость вышел из списка или его исключили.
    func leave(_ owner: String) { drop(owner: owner) }

    /// Приглашение открыли — принимаем и сразу забираем список.
    static func accept(_ metadata: CKShare.Metadata) {
        Task {
            do {
                _ = try await CKContainer(identifier: containerID).accept(metadata)
                try await shared.sharedEngine?.fetchChanges()
            } catch {
                print("Не удалось принять приглашение: \(error)")
            }
        }
    }

    private static func order(_ a: Purchase, _ b: Purchase) -> Bool {
        if a.isBought != b.isBought { return !a.isBought }
        let rank: [Purchase.Kind: Int] = [.listed: 0, .out: 1, .low: 2]
        if a.kind != b.kind { return rank[a.kind, default: 0] < rank[b.kind, default: 0] }
        return a.title.localizedStandardCompare(b.title) == .orderedAscending
    }

    private func name(of owner: String) -> String {
        if let name = demoNames[owner] { return name }
        if let components = shares[owner]?.owner.userIdentity.nameComponents {
            let name = PersonNameComponentsFormatter.localizedString(from: components, style: .short)
            if !name.isEmpty { return name }
        }
        return String(localized: "Владелец списка")
    }

    // MARK: Записи

    private func recordID(for purchase: Purchase) -> CKRecord.ID {
        CKRecord.ID(recordName: purchase.key,
                    zoneID: CKRecordZone.ID(zoneName: Self.zoneName, ownerName: purchase.zoneOwner))
    }

    private static func id(of recordID: CKRecord.ID) -> String {
        recordID.zoneID.ownerName + "/" + recordID.recordName
    }

    private func record(for purchase: Purchase) -> CKRecord {
        var record: CKRecord?
        if let data = purchase.systemFields, let coder = try? NSKeyedUnarchiver(forReadingFrom: data) {
            coder.requiresSecureCoding = true
            record = CKRecord(coder: coder)
            coder.finishDecoding()
        }
        let result = record ?? CKRecord(recordType: Self.recordType, recordID: recordID(for: purchase))
        result["title"] = purchase.title
        result["amount"] = purchase.amount
        result["unit"] = purchase.unit
        result["kind"] = purchase.kind.rawValue
        result["isBought"] = purchase.isBought
        result["byGuest"] = purchase.byGuest
        result["imported"] = purchase.imported
        result["applied"] = purchase.applied
        return result
    }

    private func receive(_ record: CKRecord) {
        let owner = record.recordID.zoneID.ownerName
        if let share = record as? CKShare {
            shares[owner] = share
            return
        }
        guard record.recordType == Self.recordType else { return }
        let coder = NSKeyedArchiver(requiringSecureCoding: true)
        record.encodeSystemFields(with: coder)
        coder.finishEncoding()
        let purchase = Purchase(
            zoneOwner: owner, key: record.recordID.recordName,
            title: record["title"] as? String ?? "",
            amount: record["amount"] as? Double ?? 0,
            unit: record["unit"] as? String ?? "",
            kind: (record["kind"] as? String).flatMap(Purchase.Kind.init) ?? .listed,
            isBought: record["isBought"] as? Bool ?? false,
            byGuest: record["byGuest"] as? Bool ?? false,
            imported: record["imported"] as? Bool ?? false,
            applied: record["applied"] as? Bool ?? false,
            systemFields: coder.encodedData)
        items[purchase.id] = purchase
    }

    private func drop(owner: String) {
        items = items.filter { $0.value.zoneOwner != owner }
        shares[owner] = nil
        claims = claims.filter { !$0.key.hasPrefix(owner + "/") }
        persist()
    }

    // MARK: CKSyncEngine

    private func handle(_ event: CKSyncEngine.Event, from engine: CKSyncEngine) {
        let isShared = engine === sharedEngine
        switch event {
        case .stateUpdate(let update):
            if isShared {
                states.sharedDatabase = update.stateSerialization
            } else {
                states.privateDatabase = update.stateSerialization
            }
            persist()

        case .accountChange(let change):
            // Вышли из iCloud или сменили учётную запись — чужие списки и
            // записи прежнего владельца показывать нельзя.
            if case .signIn = change.changeType { return }
            items = [:]
            shares = [:]
            claims = [:]
            FamilyKitchen.shared.reset()
            persist()

        case .fetchedDatabaseChanges(let changes):
            for deletion in changes.deletions where deletion.zoneID.zoneName == Self.zoneName {
                drop(owner: deletion.zoneID.ownerName)
            }
            for deletion in changes.deletions where deletion.zoneID.zoneName == FamilyKitchen.zoneName {
                FamilyKitchen.shared.zoneDeleted(owner: deletion.zoneID.ownerName)
            }

        case .fetchedRecordZoneChanges(let changes):
            // Зона `Kitchen` — семейная кухня, у неё свой разбор.
            let kitchen = FamilyKitchen.shared
            for modification in changes.modifications {
                if modification.record.recordID.zoneID.zoneName == FamilyKitchen.zoneName {
                    kitchen.receive(modification.record)
                } else {
                    receive(modification.record)
                }
            }
            for deletion in changes.deletions {
                if deletion.recordID.zoneID.zoneName == FamilyKitchen.zoneName {
                    kitchen.deleted(deletion.recordID, type: deletion.recordType)
                } else if deletion.recordType == CKRecord.SystemType.share {
                    let owner = deletion.recordID.zoneID.ownerName
                    // Владелец закрыл доступ с другого устройства — зона тоже не нужна.
                    if owner == CKCurrentUserDefaultName { stopSharing() } else { drop(owner: owner) }
                } else {
                    items[Self.id(of: deletion.recordID)] = nil
                }
            }
            kitchen.flush()
            reconcile()
            persist()

        case .sentRecordZoneChanges(let sent):
            for record in sent.savedRecords {
                if record.recordID.zoneID.zoneName == FamilyKitchen.zoneName {
                    FamilyKitchen.shared.saved(record)
                    continue
                }
                receive(record)
                let id = Self.id(of: record.recordID)
                if let claim = claims.removeValue(forKey: id), let purchase = items[id], let context {
                    Self.apply(claim, purchase, in: context)
                }
            }
            for failure in sent.failedRecordSaves {
                if failure.record.recordID.zoneID.zoneName == FamilyKitchen.zoneName {
                    FamilyKitchen.shared.failed(failure.record, failure.error, engine: engine)
                } else {
                    handleFailure(failure.record, failure.error, engine: engine)
                }
            }
            reconcile()
            persist()

        default:
            break
        }
    }

    private func handleFailure(_ record: CKRecord, _ error: CKError, engine: CKSyncEngine) {
        let id = Self.id(of: record.recordID)
        let mine = items[id]
        switch error.code {
        case .serverRecordChanged:
            // Запись успел изменить кто-то другой. Своя заявка проиграла —
            // её проведёт победитель; правки владельца `reconcile` пересчитает.
            claims[id] = nil
            guard let server = error.serverRecord else { return }
            receive(server)
            // Отметка гостя — единственное, что он меняет: накладываем её
            // на свежую версию и отправляем снова.
            if let mine, !mine.isOwn, var fresh = items[id], !fresh.applied, fresh.isBought != mine.isBought {
                fresh.isBought = mine.isBought
                items[id] = fresh
                engine.state.add(pendingRecordZoneChanges: [.saveRecord(record.recordID)])
            }
        case .unknownItem:
            // Запись удалили на сервере: своя пересоздастся, если ещё нужна.
            claims[id] = nil
            if mine?.isOwn == true {
                items[id]?.systemFields = nil
            } else {
                items[id] = nil
            }
        case .zoneNotFound:
            if mine?.isOwn == true {
                engine.state.add(pendingDatabaseChanges: [.saveZone(CKRecordZone(zoneName: Self.zoneName))])
                engine.state.add(pendingRecordZoneChanges: [.saveRecord(record.recordID)])
            } else {
                drop(owner: record.recordID.zoneID.ownerName)
            }
        default:
            // Сеть, лимиты, занятый сервер — CKSyncEngine повторит сам.
            break
        }
    }

    private func batch(_ context: CKSyncEngine.SendChangesContext,
                       engine: CKSyncEngine) -> CKSyncEngine.RecordZoneChangeBatch? {
        var save: [CKRecord] = []
        var delete: [CKRecord.ID] = []
        for change in engine.state.pendingRecordZoneChanges where context.options.scope.contains(change) {
            switch change {
            case .saveRecord(let recordID) where recordID.zoneID.zoneName == FamilyKitchen.zoneName:
                if let record = FamilyKitchen.shared.record(for: recordID) {
                    save.append(record)
                } else {
                    engine.state.remove(pendingRecordZoneChanges: [change])
                }
            case .saveRecord(let recordID):
                if let purchase = items[Self.id(of: recordID)] {
                    save.append(record(for: purchase))
                } else {
                    engine.state.remove(pendingRecordZoneChanges: [change])
                }
            case .deleteRecord(let recordID):
                delete.append(recordID)
            @unknown default:
                break
            }
        }
        guard !save.isEmpty || !delete.isEmpty else { return nil }
        return CKSyncEngine.RecordZoneChangeBatch(recordsToSave: save, recordIDsToDelete: delete)
    }

    // MARK: Хранение

    private static var fileURL: URL {
        URL.applicationSupportDirectory.appending(path: "SharedLists.json")
    }

    // ponytail: весь кеш одним JSON на каждое событие — списки маленькие; база, если станут большими.
    private func persist() {
        let stored = Stored(states: states, items: Array(items.values), shares: shares.compactMapValues {
            try? NSKeyedArchiver.archivedData(withRootObject: $0, requiringSecureCoding: true)
        })
        guard let data = try? JSONEncoder().encode(stored) else { return }
        try? FileManager.default.createDirectory(at: URL.applicationSupportDirectory,
                                                 withIntermediateDirectories: true)
        try? data.write(to: Self.fileURL, options: .atomic)
    }

    private func load() {
        guard let data = try? Data(contentsOf: Self.fileURL),
              let stored = try? JSONDecoder().decode(Stored.self, from: data) else { return }
        states = stored.states
        items = Dictionary(stored.items.map { ($0.id, $0) }, uniquingKeysWith: { _, last in last })
        shares = stored.shares.compactMapValues {
            try? NSKeyedUnarchiver.unarchivedObject(ofClass: CKShare.self, from: $0)
        }
    }
}

extension SharedLists: CKSyncEngineDelegate {
    nonisolated func handleEvent(_ event: CKSyncEngine.Event, syncEngine: CKSyncEngine) async {
        await handle(event, from: syncEngine)
    }

    nonisolated func nextRecordZoneChangeBatch(_ context: CKSyncEngine.SendChangesContext,
                                               syncEngine: CKSyncEngine) async -> CKSyncEngine.RecordZoneChangeBatch? {
        await batch(context, engine: syncEngine)
    }

    /// В приватной базе живёт и зона SwiftData со всем холодильником — её
    /// синхронизирует сама SwiftData, а мы забираем только свою зону.
    nonisolated func nextFetchChangesOptions(_ context: CKSyncEngine.FetchChangesContext,
                                             syncEngine: CKSyncEngine) async -> CKSyncEngine.FetchChangesOptions {
        guard syncEngine.database.databaseScope == .private else { return context.options }
        var options = context.options
        options.scope = .zoneIDs([CKRecordZone.ID(zoneName: SharedLists.zoneName),
                                  CKRecordZone.ID(zoneName: FamilyKitchen.zoneName)])
        return options
    }
}

/// Держит свою зону общего списка в согласии с холодильником и списком:
/// вешается на корневой экран приложения и часов.
struct SharedListSync: ViewModifier {
    @Query private var stock: [StockItem]
    @Query private var listed: [ShoppingItem]

    private var signature: [String] {
        Kitchen.shoppingEntries(from: stock).map { "\($0.item.name)|\($0.suggested)|\($0.state)" }
            + listed.map { "\($0.name)|\($0.amount)|\($0.isBought)" }
    }

    func body(content: Content) -> some View {
        content.onChange(of: signature, initial: true) { SharedLists.shared.reconcile() }
    }
}

#if !os(watchOS)
/// Приглашение в общий список для ShareLink: зона и CKShare создаются,
/// только когда приглашение действительно отправляют.
struct SharedListInvite: Transferable {
    static var transferRepresentation: some TransferRepresentation {
        CKShareTransferRepresentation { _ in
            .prepareShare(container: CKContainer(identifier: SharedLists.containerID),
                          allowedSharingOptions: CKAllowedSharingOptions(
                              allowedParticipantPermissionOptions: .readWrite,
                              allowedParticipantAccessOptions: .specifiedRecipientsOnly)) {
                try await SharedLists.makeShare()
            }
        }
    }
}
#endif

extension Purchase {
    /// Подпись под названием: сколько взять и почему оно в списке.
    var detail: String? {
        let amount = amount > 0 ? Fmt.amount(amount, unit: unit).trimmingCharacters(in: .whitespaces) : nil
        let reason: String? = switch kind {
        case .listed: nil
        case .out: String(localized: "Закончилось")
        case .low: String(localized: "На исходе")
        }
        let parts = [amount, reason].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}
