import SwiftData
import SwiftUI

/// Что закончилось или заканчивается — и три способа с этим что-то сделать.
struct ShoppingListView: View {
    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]

    @State private var isExporting = false
    @State private var alert: AlertPayload?

    private struct AlertPayload: Identifiable {
        let id = UUID()
        let title: String
        let message: String
    }

    private var entries: [Kitchen.ShoppingEntry] {
        stock
            .filter { $0.product != nil && ($0.isEmpty || $0.isLow) }
            .map { Kitchen.ShoppingEntry(item: $0, state: $0.isEmpty ? .out : .low) }
            .sorted {
                if ($0.state == .out) != ($1.state == .out) { return $0.state == .out }
                return $0.item.name.localizedCaseInsensitiveCompare($1.item.name) == .orderedAscending
            }
    }

    private var out: [Kitchen.ShoppingEntry] { entries.filter { $0.state == .out } }
    private var low: [Kitchen.ShoppingEntry] { entries.filter { $0.state == .low } }

    var body: some View {
        NavigationStack {
            Group {
                if entries.isEmpty {
                    EmptyStateView(symbol: "checkmark.circle",
                                   title: "Покупать нечего",
                                   message: "Всё, что нужно рецептам, есть в холодильнике. Список наполнится сам, когда продукты начнут заканчиваться.")
                } else {
                    List {
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
                    }
                }
            }
            .navigationTitle("Для покупки")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    ShareLink(item: Kitchen.shareText(for: entries)) {
                        Label("Поделиться", systemImage: "square.and.arrow.up")
                    }
                    .disabled(entries.isEmpty)
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
                    .disabled(entries.isEmpty || isExporting)
                }
            }
            .alert(item: $alert) { payload in
                Alert(title: Text(payload.title), message: Text(payload.message),
                      dismissButton: .default(Text("Понятно")))
            }
        }
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
        let suggested = "взять \(product.amountLabel(entry.suggested))"
        return entry.state == .out
            ? "нет в наличии · \(suggested)"
            : "осталось \(product.amountLabel(entry.item.currentAmount)) · \(suggested)"
    }

    private func exportToReminders() {
        let titles = entries.map(Kitchen.purchaseTitle)
        isExporting = true
        Task {
            defer { isExporting = false }
            do {
                let result = try await RemindersService.shared.export(titles: titles)
                Feedback.shared.play(.cooked)
                var message = "Список «\(RemindersService.listName)» обновлён: добавлено \(result.added)."
                if result.alreadyThere > 0 {
                    message += " Уже были в списке: \(result.alreadyThere)."
                }
                alert = AlertPayload(title: "Готово", message: message)
            } catch {
                Feedback.shared.play(.warning)
                alert = AlertPayload(title: "Не получилось",
                                     message: error.localizedDescription)
            }
        }
    }
}
