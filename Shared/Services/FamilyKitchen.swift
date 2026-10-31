import CloudKit
import Foundation
import Observation
import SwiftData
import SwiftUI

/// Семейная кухня: владелец открывает свою кухню другим людям через iCloud —
/// только холодильник или всё. Кухня лежит в зоне `Kitchen` его приватной
/// базы (CKShare на зону), каждая запись — объект SwiftData (`KitchenMirror`).
/// У гостя чужая кухня — отдельная локальная база; в настройках он выбирает,
/// какая кухня открыта: своя или семейная. Правки идут в обе стороны.
///
/// Движки CKSyncEngine общие с `SharedLists` (один движок на базу iCloud) —
/// он передаёт сюда всё, что касается зоны `Kitchen`.
@Observable
final class FamilyKitchen {
    static let shared = FamilyKitchen()
    nonisolated static let zoneName = "Kitchen"
    static let activeKey = "activeKitchen"
    private static let recordType = "KitchenRecord"

    struct Joined: Identifiable, Codable, Hashable {
        var owner: String
        var name: String
        var access: KitchenAccess
        var id: String { owner }
    }

    private struct Entry: Codable {
        var record: MirrorRecord
        var systemFields: Data?
    }

    private struct Stored: Codable {
        var ownAccess: KitchenAccess
        var ownShare: Data?
        var joined: [String: Joined]
        var guestShares: [String: Data]
        var sent: [String: [String: Entry]]
    }

    private(set) var ownShare: CKShare?
    private(set) var ownAccess: KitchenAccess = .everything
    private(set) var joined: [String: Joined] = [:]

    @ObservationIgnored private(set) var guestShares: [String: CKShare] = [:]
    @ObservationIgnored private var sent: [String: [String: Entry]] = [:]
    @ObservationIgnored private var received: [String: [MirrorRecord]] = [:]
    @ObservationIgnored private var photos: [String: Data] = [:]
    @ObservationIgnored private var deletions: [String: [String]] = [:]
    /// Пришло, но ещё не легло в базу (ингредиент раньше своего рецепта):
    /// повторяем на следующей порции и не считаем удалённым.
    @ObservationIgnored private var unresolved: [String: [MirrorRecord]] = [:]
    @ObservationIgnored private var containers: [String: ModelContainer] = [:]
    @ObservationIgnored private var ownContext: ModelContext?

    private static let own = CKCurrentUserDefaultName

    func start(context: ModelContext) {
        ownContext = context
        load()
    }

    var isShared: Bool { ownShare != nil }

    /// База чужой кухни на этом устройстве — без iCloud: синхронизирует её
    /// зона владельца.
    func container(for owner: String) -> ModelContainer {
        if let container = containers[owner] { return container }
        let name = "Family-" + owner.filter { $0.isLetter || $0.isNumber }
        let configuration = ModelConfiguration(name, schema: KitchenStore.schema, groupContainer: .none,
                                               cloudKitDatabase: .none)
        let container = (try? ModelContainer(for: KitchenStore.schema, configurations: configuration))
            ?? KitchenStore.makeContainer(isTestRun: true)
        containers[owner] = container
        return container
    }

    private func context(for owner: String) -> ModelContext? {
        owner == Self.own ? ownContext : container(for: owner).mainContext
    }

    private func access(for owner: String) -> KitchenAccess {
        owner == Self.own ? ownAccess : joined[owner]?.access ?? .fridge
    }

    // MARK: Владелец

    static func makeShare(access: KitchenAccess) async throws -> CKShare {
        let kitchen = FamilyKitchen.shared
        if let share = kitchen.ownShare { return share }
        guard let container = SharedLists.shared.container else { throw CKError(.notAuthenticated) }
        let database = container.privateCloudDatabase
        let zone = CKRecordZone(zoneName: zoneName)
        _ = try await database.modifyRecordZones(saving: [zone], deleting: [])
        let share = CKShare(recordZoneID: zone.zoneID)
        share[CKShare.SystemFieldKey.title] = String(localized: "Семейная кухня")
        share.publicPermission = .none
        let result = try await database.modifyRecords(saving: [share], deleting: [])
        guard let saved = try result.saveResults[share.recordID]?.get() as? CKShare else {
            throw CKError(.internalError)
        }
        kitchen.ownAccess = access
        kitchen.ownShare = saved
        kitchen.persist()
        kitchen.reconcile()
        return saved
    }

    /// Владелец закрыл доступ: зона удаляется, гости теряют кухню.
    func stopSharing() {
        SharedLists.shared.privateEngine?.state.add(pendingDatabaseChanges: [.deleteZone(CKRecordZone.ID(zoneName: Self.zoneName))])
        dropOwn()
    }

    /// Гость вышел сам или его исключили.
    func leave(_ owner: String) {
        dropGuest(owner)
    }

    // MARK: Отправка

    /// Привести зоны к базам: своя кухня — если открыта, чужие — у гостя.
    func reconcile() {
        if ownShare != nil { reconcile(owner: Self.own) }
        for owner in joined.keys { reconcile(owner: owner) }
    }

    private func reconcile(owner: String) {
        guard let context = context(for: owner),
              let engine = owner == Self.own ? SharedLists.shared.privateEngine : SharedLists.shared.sharedEngine
        else { return }
        let access = access(for: owner)
        let allowed = Set(KitchenMirror.types(for: access))
        var current = KitchenMirror.export(context, access: access)
        if owner == Self.own {
            current.append(MirrorRecord(type: "KitchenInfo", uid: "info", fields: ["access": .text(access.rawValue)]))
        }
        let waiting = Set((unresolved[owner] ?? []).map(\.recordName))
        let known = (sent[owner] ?? [:]).filter { name, entry in
            (allowed.contains(entry.record.type) || entry.record.type == "KitchenInfo" && owner == Self.own)
                && !waiting.contains(name)
        }
        let plan = KitchenMirror.diff(current: current, sent: known.mapValues(\.record))
        guard !plan.save.isEmpty || !plan.delete.isEmpty else { return }
        let zoneID = CKRecordZone.ID(zoneName: Self.zoneName, ownerName: owner)
        for record in plan.save {
            sent[owner, default: [:]][record.recordName] = Entry(record: record,
                                                                 systemFields: sent[owner]?[record.recordName]?.systemFields)
        }
        for name in plan.delete { sent[owner]?[name] = nil }
        engine.state.add(pendingRecordZoneChanges:
            plan.save.map { .saveRecord(CKRecord.ID(recordName: $0.recordName, zoneID: zoneID)) }
            + plan.delete.map { .deleteRecord(CKRecord.ID(recordName: $0, zoneID: zoneID)) })
        persist()
    }

    /// Запись для отправки: системные поля последней версии + payload + фото.
    func record(for id: CKRecord.ID) -> CKRecord? {
        let owner = id.zoneID.ownerName
        guard let entry = sent[owner]?[id.recordName] else { return nil }
        var record: CKRecord?
        if let data = entry.systemFields, let coder = try? NSKeyedUnarchiver(forReadingFrom: data) {
            coder.requiresSecureCoding = true
            record = CKRecord(coder: coder)
            coder.finishDecoding()
        }
        let result = record ?? CKRecord(recordType: Self.recordType, recordID: id)
        result["type"] = entry.record.type
        result["payload"] = (try? JSONEncoder().encode(entry.record.fields)).flatMap { String(data: $0, encoding: .utf8) }
        if entry.record.type == "Recipe", case .text = entry.record.fields["photo"],
           let context = context(for: owner),
           let photo = ((try? context.fetch(FetchDescriptor<Recipe>())) ?? []).first(where: { $0.uid == entry.record.uid })?.photoData {
            let file = URL.temporaryDirectory.appending(path: id.recordName + ".jpg")
            try? photo.write(to: file)
            result["photo"] = CKAsset(fileURL: file)
        } else {
            result["photo"] = nil
        }
        return result
    }

    func saved(_ record: CKRecord) {
        let owner = record.recordID.zoneID.ownerName
        if let share = record as? CKShare { return remember(share: share, owner: owner) }
        sent[owner]?[record.recordID.recordName]?.systemFields = Self.systemFields(of: record)
        persist()
    }

    func failed(_ record: CKRecord, _ error: CKError, engine: CKSyncEngine) {
        let owner = record.recordID.zoneID.ownerName
        switch error.code {
        case .serverRecordChanged:
            // Кто-то успел раньше. Побеждает последняя правка — наша: берём
            // свежие системные поля и отправляем снова.
            if let server = error.serverRecord {
                sent[owner]?[record.recordID.recordName]?.systemFields = Self.systemFields(of: server)
            }
            engine.state.add(pendingRecordZoneChanges: [.saveRecord(record.recordID)])
        case .unknownItem:
            sent[owner]?[record.recordID.recordName]?.systemFields = nil
            engine.state.add(pendingRecordZoneChanges: [.saveRecord(record.recordID)])
        case .zoneNotFound:
            if owner == Self.own { dropOwn() } else { dropGuest(owner) }
        default:
            break
        }
        persist()
    }

    // MARK: Приём

    func receive(_ record: CKRecord) {
        let owner = record.recordID.zoneID.ownerName
        if let share = record as? CKShare { return remember(share: share, owner: owner) }
        guard record.recordType == Self.recordType, let type = record["type"] as? String,
              let payload = (record["payload"] as? String)?.data(using: .utf8),
              let fields = try? JSONDecoder().decode([String: MirrorValue].self, from: payload) else { return }
        let name = record.recordID.recordName
        let uid = String(name.dropFirst(type.count + 1))
        let mirror = MirrorRecord(type: type, uid: uid, fields: fields)
        if type == "KitchenInfo" {
            let access = (fields["access"]).flatMap { if case .text(let raw) = $0 { KitchenAccess(rawValue: raw) } else { nil } }
            if owner == Self.own { ownAccess = access ?? ownAccess } else if let access { joined[owner]?.access = access }
        } else if sent[owner]?[name]?.record != mirror {
            received[owner, default: []].append(mirror)
            if let asset = record["photo"] as? CKAsset, let url = asset.fileURL, let data = try? Data(contentsOf: url) {
                photos[name] = data
            }
        }
        sent[owner, default: [:]][name] = Entry(record: mirror, systemFields: Self.systemFields(of: record))
    }

    func deleted(_ id: CKRecord.ID, type: CKRecord.RecordType) {
        let owner = id.zoneID.ownerName
        if type == CKRecord.SystemType.share {
            if owner == Self.own { dropOwn() } else { dropGuest(owner) }
            return
        }
        sent[owner]?[id.recordName] = nil
        deletions[owner, default: []].append(id.recordName)
    }

    /// Всё, что пришло порцией, — в базы разом: связи между записями порции
    /// разрешаются, а не теряются.
    func flush() {
        for owner in Set(received.keys).union(deletions.keys).union(unresolved.keys) {
            guard let context = context(for: owner) else { continue }
            let records = (unresolved[owner] ?? []) + (received[owner] ?? [])
            KitchenMirror.apply(records, photos: photos, deleted: deletions[owner] ?? [], to: context)
            // Что не легло в базу (нет рецепта или продукта) — ждёт следующей порции.
            let present = Set(KitchenMirror.export(context, access: .everything).map(\.recordName))
            unresolved[owner] = records.filter { !present.contains($0.recordName) }
        }
        received = [:]
        photos = [:]
        deletions = [:]
        persist()
    }

    func zoneDeleted(owner: String) {
        if owner == Self.own { dropOwn() } else { dropGuest(owner) }
    }

    func reset() {
        ownShare = nil
        joined = [:]
        guestShares = [:]
        sent = [:]
        unresolved = [:]
        UserDefaults.standard.removeObject(forKey: Self.activeKey)
        persist()
    }

    private func remember(share: CKShare, owner: String) {
        if owner == Self.own {
            ownShare = share
        } else {
            guestShares[owner] = share
            let components = share.owner.userIdentity.nameComponents
            let formatted = components.map { PersonNameComponentsFormatter.localizedString(from: $0, style: .short) } ?? ""
            let name = formatted.isEmpty ? String(localized: "Владелец списка") : formatted
            joined[owner] = Joined(owner: owner, name: name, access: joined[owner]?.access ?? .fridge)
        }
        persist()
    }

    private func dropOwn() {
        ownShare = nil
        sent[Self.own] = nil
        unresolved[Self.own] = nil
        persist()
    }

    private func dropGuest(_ owner: String) {
        joined[owner] = nil
        guestShares[owner] = nil
        sent[owner] = nil
        unresolved[owner] = nil
        if UserDefaults.standard.string(forKey: Self.activeKey) == owner {
            UserDefaults.standard.removeObject(forKey: Self.activeKey)
        }
        if let container = containers.removeValue(forKey: owner) {
            for store in container.configurations { try? FileManager.default.removeItem(at: store.url) }
        }
        persist()
    }

    private static func systemFields(of record: CKRecord) -> Data {
        let coder = NSKeyedArchiver(requiringSecureCoding: true)
        record.encodeSystemFields(with: coder)
        coder.finishEncoding()
        return coder.encodedData
    }

    // MARK: Хранение

    private static var fileURL: URL { URL.applicationSupportDirectory.appending(path: "FamilyKitchen.json") }

    private func persist() {
        func archive(_ share: CKShare?) -> Data? {
            share.flatMap { try? NSKeyedArchiver.archivedData(withRootObject: $0, requiringSecureCoding: true) }
        }
        let stored = Stored(ownAccess: ownAccess, ownShare: archive(ownShare), joined: joined,
                            guestShares: guestShares.compactMapValues(archive), sent: sent)
        guard let data = try? JSONEncoder().encode(stored) else { return }
        try? FileManager.default.createDirectory(at: URL.applicationSupportDirectory, withIntermediateDirectories: true)
        try? data.write(to: Self.fileURL, options: .atomic)
    }

    private func load() {
        guard let data = try? Data(contentsOf: Self.fileURL),
              let stored = try? JSONDecoder().decode(Stored.self, from: data) else { return }
        func unarchive(_ data: Data?) -> CKShare? {
            data.flatMap { try? NSKeyedUnarchiver.unarchivedObject(ofClass: CKShare.self, from: $0) }
        }
        ownAccess = stored.ownAccess
        ownShare = unarchive(stored.ownShare)
        joined = stored.joined
        guestShares = stored.guestShares.compactMapValues(unarchive)
        sent = stored.sent
    }
}

#if !os(watchOS)
/// Приглашение в кухню для ShareLink — с выбранным доступом.
struct KitchenInvite: Transferable {
    let access: KitchenAccess

    static var transferRepresentation: some TransferRepresentation {
        CKShareTransferRepresentation { invite in
            .prepareShare(container: CKContainer(identifier: SharedLists.containerID),
                          allowedSharingOptions: CKAllowedSharingOptions(
                              allowedParticipantPermissionOptions: .readWrite,
                              allowedParticipantAccessOptions: .specifiedRecipientsOnly)) {
                try await FamilyKitchen.makeShare(access: invite.access)
            }
        }
    }
}
#endif
