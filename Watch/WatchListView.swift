import SwiftData
import SwiftUI
import WatchKit

struct WatchListView: View {
    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]
    @Query(sort: \ShoppingItem.createdAt) private var listed: [ShoppingItem]

    private var lists: SharedLists { .shared }
    private var entries: [Kitchen.ShoppingEntry] { Kitchen.shoppingEntries(from: stock) }

    var body: some View {
        NavigationStack {
            List {
                if !listed.isEmpty {
                    Section("Мой список") {
                        ForEach(listed) { item in
                            Button {
                                Kitchen.setBought(item, !item.isBought, in: context)
                                tap(item.isBought)
                            } label: {
                                row(item.name, detail: item.amount > 0 ? item.product?.amountLabel(item.amount) : nil,
                                    isBought: item.isBought)
                            }
                        }
                    }
                }
                ForEach([Kitchen.ShoppingState.out, .low], id: \.self) { state in
                    let rows = entries.filter { $0.state == state }
                    if !rows.isEmpty {
                        Section(state == .out ? LocalizedStringKey("Закончилось") : "На исходе") {
                            ForEach(rows) { entry in
                                Button {
                                    Kitchen.replenish(entry.item, by: entry.suggested)
                                    try? context.save()
                                    tap(true)
                                } label: {
                                    row(entry.item.name, detail: entry.item.product?.amountLabel(entry.suggested),
                                        isBought: false)
                                }
                            }
                        }
                    }
                }
                ForEach(lists.guestLists) { list in
                    Section("Общий список: \(list.name)") {
                        ForEach(list.items) { purchase in
                            Button {
                                lists.toggle(purchase)
                                tap(!purchase.isBought)
                            } label: {
                                row(purchase.title, detail: purchase.detail, isBought: purchase.isBought)
                            }
                            .disabled(purchase.applied)
                        }
                    }
                }
            }
            .overlay {
                if listed.isEmpty && entries.isEmpty && lists.guestLists.isEmpty {
                    ContentUnavailableView("Покупать нечего", systemImage: "checkmark.circle")
                }
            }
            .navigationTitle("Для покупки")
        }
    }

    private func row(_ title: String, detail: String?, isBought: Bool) -> some View {
        HStack(spacing: 8) {
            Image(systemName: isBought ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(isBought ? Color.green : Color.secondary)
            VStack(alignment: .leading, spacing: 1) {
                Text(title)
                    .strikethrough(isBought)
                    .foregroundStyle(isBought ? .secondary : .primary)
                if let detail {
                    Text(detail)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func tap(_ bought: Bool) {
        WKInterfaceDevice.current().play(bought ? .success : .click)
    }
}

extension Kitchen.ShoppingState: Hashable {}
