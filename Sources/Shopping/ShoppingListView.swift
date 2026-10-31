import CloudKit
import SwiftData
import SwiftUI

/// Что купить: своё, вписанное руками, и то, что закончилось или заканчивается
/// в холодильнике. Всё синхронизируется через iCloud — список, начатый на
/// iPad, отмечается в магазине с iPhone. Ниже — общие списки других людей,
/// которые пригласили в них через iCloud.
struct ShoppingListView: View {
    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]
    @Query(sort: \ShoppingItem.createdAt) private var listed: [ShoppingItem]

    @State private var newItem = ""
    @State private var isScanning = false
    @State private var isExporting = false
    @State private var alert: AlertPayload?
    @State private var managing: CKShare?
    @State private var guestDrafts: [String: String] = [:]
    @FocusState private var isAddFocused: Bool

    private struct AlertPayload: Identifiable {
        let id = UUID()
        let title: String
        let message: String
    }

    private var entries: [Kitchen.ShoppingEntry] { Kitchen.shoppingEntries(from: stock) }
    private var out: [Kitchen.ShoppingEntry] { entries.filter { $0.state == .out } }
    private var low: [Kitchen.ShoppingEntry] { entries.filter { $0.state == .low } }
    private var toBuy: [ShoppingItem] { listed.filter { !$0.isBought } }
    private var isEmpty: Bool { entries.isEmpty && toBuy.isEmpty }
    private var lists: SharedLists { .shared }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    addRow
                } footer: {
                    Text("Впишите что угодно или отсканируйте штрихкод. Если это продукт из холодильника, «Купил» пополнит его остаток.")
                }

                if !listed.isEmpty {
                    Section("Мой список") {
                        ForEach(listed) { listedRow($0) }
                            .onDelete { offsets in
                                for index in offsets { context.delete(listed[index]) }
                                try? context.save()
                            }
                    }
                }
                // Общие списки — сразу за своим: их, как и свой, ведут люди, а
                // ниже — то, что приложение собрало само.
                ForEach(lists.guestLists) { guestSection($0) }
                if !out.isEmpty {
                    Section("Закончилось") {
                        ForEach(out) { row($0) }
                    }
                }
                if !low.isEmpty {
                    Section("На исходе") {
                        ForEach(low) { row($0) }
                    }
                }
                if isEmpty && lists.guestLists.isEmpty {
                    EmptyStateView(symbol: "checkmark.circle",
                                   title: "Покупать нечего",
                                   message: "Всё, что нужно рецептам, есть в холодильнике. Список наполнится сам, когда продукты начнут заканчиваться.")
                        .listRowBackground(Color.clear)
                }
            }
            .navigationTitle("Для покупки")
            .sheet(isPresented: $isScanning) { ScanSheet() }
            .sheet(item: $managing) { CloudSharingView(share: $0).ignoresSafeArea() }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    shareMenu
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        exportToReminders()
                    } label: {
                        if isExporting {
                            ProgressView()
                        } else {
                            Label("В Напоминания", systemImage: "checklist")
                        }
                    }
                    .disabled(isEmpty || isExporting)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button {
                            Kitchen.buyAll(entries, in: context)
                            Feedback.shared.play(.added)
                        } label: {
                            Label("Купил всё из холодильника", systemImage: "cart.badge.plus")
                        }
                        .disabled(entries.isEmpty)
                        Button(role: .destructive) {
                            Kitchen.removeBought(in: context)
                        } label: {
                            Label("Убрать купленное", systemImage: "checkmark.circle.badge.xmark")
                        }
                        .disabled(!listed.contains(where: \.isBought))
                    } label: {
                        Label("Ещё", systemImage: "ellipsis.circle")
                    }
                }
            }
            .alert(item: $alert) { payload in
                Alert(title: Text(payload.title), message: Text(payload.message),
                      dismissButton: .default(Text("Понятно")))
            }
        }
    }

    /// Общий список через iCloud — тому, у кого есть приложение; текстом — кому угодно.
    private var shareMenu: some View {
        Menu {
            if lists.isAvailable {
                ShareLink(item: SharedListInvite(),
                          preview: SharePreview("Список покупок", image: Image(systemName: "cart"))) {
                    Label("Пригласить в общий список", systemImage: "person.badge.plus")
                }
                if let share = lists.ownShare {
                    Button {
                        managing = share
                    } label: {
                        Label("Кто видит список", systemImage: "person.2")
                    }
                }
            }
            ShareLink(item: Kitchen.shareText(for: entries, extra: listed)) {
                Label("Отправить текстом", systemImage: "text.alignleft")
            }
            .disabled(isEmpty)
        } label: {
            Label("Поделиться", systemImage: "square.and.arrow.up")
        }
    }

    private func guestSection(_ list: SharedLists.GuestList) -> some View {
        Section {
            ForEach(list.items) { guestRow($0) }
            HStack(spacing: 12) {
                Image(systemName: "plus.circle")
                    .font(.title3)
                    .foregroundStyle(.tint)
                TextField("Дописать в этот список", text: draft(for: list.owner))
                    .submitLabel(.done)
                    .onSubmit { addToGuest(list.owner) }
            }
        } header: {
            HStack {
                Text("Общий список: \(list.name)")
                Spacer()
                if let share = list.share {
                    Button {
                        managing = share
                    } label: {
                        Label("Кто видит список", systemImage: "person.2")
                            .labelStyle(.iconOnly)
                    }
                }
            }
        }
    }

    private func guestRow(_ purchase: Purchase) -> some View {
        Button {
            withAnimation(.snappy) { lists.toggle(purchase) }
            Feedback.shared.play(purchase.isBought ? .removed : .added)
        } label: {
            HStack(spacing: 12) {
                Image(systemName: purchase.isBought ? "checkmark.circle.fill" : "circle")
                    .font(.title3)
                    .foregroundStyle(purchase.isBought ? Color.green : Color.secondary)
                VStack(alignment: .leading, spacing: 2) {
                    Text(purchase.title)
                        .strikethrough(purchase.isBought)
                        .foregroundStyle(purchase.isBought ? .secondary : .primary)
                    if let detail = purchase.detail {
                        Text(detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .disabled(purchase.applied)
        .accessibilityAddTraits(purchase.isBought ? .isSelected : [])
    }

    private func draft(for owner: String) -> Binding<String> {
        Binding { guestDrafts[owner, default: ""] } set: { guestDrafts[owner] = $0 }
    }

    private func addToGuest(_ owner: String) {
        lists.add(guestDrafts[owner, default: ""], toListOf: owner)
        guestDrafts[owner] = ""
        Feedback.shared.play(.added)
    }

    private var addRow: some View {
        HStack(spacing: 12) {
            Image(systemName: "plus.circle.fill")
                .font(.title3)
                .foregroundStyle(.tint)
            TextField("Добавить в список", text: $newItem)
                .focused($isAddFocused)
                .submitLabel(.done)
                .onSubmit(add)
            if !newItem.trimmingCharacters(in: .whitespaces).isEmpty {
                Button("Добавить", action: add)
                    .buttonStyle(.borderless)
            } else {
                Button {
                    isScanning = true
                } label: {
                    Label("Сканировать штрихкод", systemImage: "barcode.viewfinder")
                        .labelStyle(.iconOnly)
                }
                .buttonStyle(.borderless)
            }
        }
    }

    private func add() {
        guard Kitchen.addToList(newItem, in: context) != nil else { return }
        newItem = ""
        isAddFocused = true
        Feedback.shared.play(.added)
    }

    private func listedRow(_ item: ShoppingItem) -> some View {
        Button {
            withAnimation(.snappy) { Kitchen.setBought(item, !item.isBought, in: context) }
            Feedback.shared.play(item.isBought ? .added : .removed)
        } label: {
            HStack(spacing: 12) {
                Image(systemName: item.isBought ? "checkmark.circle.fill" : "circle")
                    .font(.title3)
                    .foregroundStyle(item.isBought ? Color.green : Color.secondary)
                VStack(alignment: .leading, spacing: 2) {
                    Text(item.name)
                        .strikethrough(item.isBought)
                        .foregroundStyle(item.isBought ? .secondary : .primary)
                    if let subtitle = subtitle(for: item) {
                        Text(subtitle)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(item.isBought ? .isSelected : [])
    }

    private func subtitle(for item: ShoppingItem) -> String? {
        let amount = item.amount > 0 ? item.product?.amountLabel(item.amount) : nil
        guard item.product != nil else { return amount }
        let hint = item.isBought
            ? String(localized: "добавлено в холодильник")
            : String(localized: "пополнит холодильник")
        return [amount, hint].compactMap { $0 }.joined(separator: " · ")
    }

    private func row(_ entry: Kitchen.ShoppingEntry) -> some View {
        HStack(spacing: 12) {
            Image(systemName: CategoryStyle.symbol(for: entry.item.category))
                .font(.system(size: 14, weight: .semibold))
                .foregroundStyle(.white)
                .frame(width: 32, height: 32)
                .background(CategoryStyle.color(for: entry.item.category).gradient, in: .circle)

            VStack(alignment: .leading, spacing: 2) {
                Text(entry.item.name).font(.body.weight(.medium))
                Text(subtitle(for: entry))
                    .font(.caption)
                    .foregroundStyle(entry.state == .out ? .red : .orange)
            }

            Spacer()

            Button {
                Kitchen.replenish(entry.item, by: entry.suggested)
                try? context.save()
                Feedback.shared.play(.added)
            } label: {
                Label("Купил", systemImage: "checkmark")
                    .labelStyle(.titleAndIcon)
            }
            .buttonStyle(.borderedProminent)
            .buttonBorderShape(.capsule)
        }
        .padding(.vertical, 4)
    }

    private func subtitle(for entry: Kitchen.ShoppingEntry) -> String {
        guard let product = entry.item.product else { return "" }
        let suggested = product.amountLabel(entry.suggested)
        return entry.state == .out
            ? String(localized: "нет в наличии · взять \(suggested)")
            : String(localized: "осталось \(product.amountLabel(entry.item.currentAmount)) · взять \(suggested)")
    }

    private func exportToReminders() {
        let titles = toBuy.map(Kitchen.purchaseTitle) + entries.map(Kitchen.purchaseTitle)
        isExporting = true
        Task {
            defer { isExporting = false }
            do {
                let result = try await RemindersService.shared.export(titles: titles)
                Feedback.shared.play(.cooked)
                var message = String(localized: "Список «\(RemindersService.listName)» обновлён: добавлено \(result.added).")
                if result.alreadyThere > 0 {
                    message += " " + String(localized: "Уже были в списке: \(result.alreadyThere).")
                }
                alert = AlertPayload(title: String(localized: "Готово"), message: message)
            } catch {
                Feedback.shared.play(.warning)
                alert = AlertPayload(title: String(localized: "Не получилось"),
                                     message: error.localizedDescription)
            }
        }
    }
}
