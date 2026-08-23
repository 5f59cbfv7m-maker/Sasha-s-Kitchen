import SwiftData
import SwiftUI

/// Главный экран: что лежит в холодильнике и сколько осталось.
struct FridgeView: View {
    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]

    @State private var search = ""
    @State private var sortMode: SortMode = .category
    @State private var isAddingProduct = false
    @State private var adjusting: StockItem?

    enum SortMode: String, CaseIterable, Identifiable {
        case category = "По категориям"
        case running = "Сначала заканчивается"
        case expiry = "По сроку годности"
        case name = "По названию"

        var id: String { rawValue }
        var symbol: String {
            switch self {
            case .category: "square.grid.2x2"
            case .running: "chart.bar.doc.horizontal"
            case .expiry: "clock"
            case .name: "textformat.abc"
            }
        }
    }

    private var items: [StockItem] {
        let query = search.trimmingCharacters(in: .whitespaces)
        return stock.filter { item in
            guard item.product != nil else { return false }
            guard !query.isEmpty else { return true }
            return item.name.localizedCaseInsensitiveContains(query)
                || item.category.localizedCaseInsensitiveContains(query)
        }
    }

    private var sorted: [StockItem] {
        switch sortMode {
        case .category:
            items.sorted {
                let (l, r) = (CategoryStyle.rank($0.category), CategoryStyle.rank($1.category))
                if l != r { return l < r }
                return $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
            }
        case .running:
            items.sorted { $0.progress < $1.progress }
        case .expiry:
            items.sorted {
                ($0.daysUntilExpiry ?? .max, $0.name) < ($1.daysUntilExpiry ?? .max, $1.name)
            }
        case .name:
            items.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        }
    }

    private var grouped: [(category: String, items: [StockItem])] {
        Dictionary(grouping: sorted, by: \.category)
            .map { (category: $0.key, items: $0.value) }
            .sorted { CategoryStyle.rank($0.category) < CategoryStyle.rank($1.category) }
    }

    private let columns = [GridItem(.adaptive(minimum: 270, maximum: 460), spacing: 14)]

    var body: some View {
        NavigationStack {
            ScrollView {
                summaryBar
                LazyVGrid(columns: columns, alignment: .leading, spacing: 14) {
                    if sortMode == .category {
                        ForEach(grouped, id: \.category) { group in
                            Section {
                                cards(group.items)
                            } header: {
                                sectionHeader(group.category, count: group.items.count)
                            }
                        }
                    } else {
                        cards(sorted)
                    }
                }
                .padding(.horizontal)
                .padding(.bottom, 40)

                if items.isEmpty {
                    EmptyStateView(
                        symbol: search.isEmpty ? "refrigerator" : "magnifyingglass",
                        title: search.isEmpty ? "Холодильник пуст" : "Ничего не нашлось",
                        message: search.isEmpty
                            ? "Добавьте первый продукт кнопкой «плюс» в правом верхнем углу."
                            : "Попробуйте другое название или категорию.")
                }
            }
            .navigationTitle("Холодильник")
            .searchable(text: $search, prompt: "Найти продукт")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        isAddingProduct = true
                    } label: {
                        Label("Добавить продукт", systemImage: "plus")
                    }
                }
            }
            .sheet(isPresented: $isAddingProduct) { AddProductView() }
            .sheet(item: $adjusting) { item in AdjustStockView(item: item) }
        }
    }

    @ViewBuilder
    private func cards(_ list: [StockItem]) -> some View {
        ForEach(list) { item in
            StockCard(item: item)
                .onTapGesture { adjusting = item }
                .contextMenu {
                    Button {
                        Kitchen.replenish(item, by: Kitchen.suggestedPurchase(for: item))
                        Feedback.shared.play(.added)
                    } label: {
                        Label("Пополнить", systemImage: "plus.circle")
                    }
                    Button { adjusting = item } label: {
                        Label("Изменить остаток", systemImage: "slider.horizontal.3")
                    }
                    Button(role: .destructive) {
                        delete(item)
                    } label: {
                        Label("Удалить продукт", systemImage: "trash")
                    }
                }
        }
    }

    private func sectionHeader(_ category: String, count: Int) -> some View {
        HStack(spacing: 8) {
            Image(systemName: CategoryStyle.symbol(for: category))
                .foregroundStyle(CategoryStyle.color(for: category))
            Text(category).font(.headline)
            Text("\(count)")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 7)
                .padding(.vertical, 2)
                .background(.quaternary, in: .capsule)
            Spacer()
        }
        .padding(.top, 8)
    }

    private var summaryBar: some View {
        let total = stock.filter { $0.product != nil }
        let empty = total.filter(\.isEmpty).count
        let low = total.filter(\.isLow).count
        let expiring = total.filter { !$0.isEmpty && $0.isExpiringSoon }.count
        return HStack(spacing: 10) {
            summaryChip("\(total.count) продуктов", symbol: "shippingbox", color: .secondary)
            if low > 0 { summaryChip("\(low) на исходе", symbol: "exclamationmark.circle", color: .orange) }
            if empty > 0 { summaryChip("\(empty) закончилось", symbol: "xmark.circle", color: .red) }
            if expiring > 0 { summaryChip("\(expiring) скоро испортится", symbol: "clock", color: .yellow) }
            Spacer()
            sortMenu
        }
        .padding(.horizontal)
        .padding(.bottom, 10)
    }

    private var sortMenu: some View {
        Menu {
            Picker("Сортировка", selection: $sortMode) {
                ForEach(SortMode.allCases) { mode in
                    Label(mode.rawValue, systemImage: mode.symbol).tag(mode)
                }
            }
        } label: {
            Label(sortMode.rawValue, systemImage: "arrow.up.arrow.down")
                .font(.caption.weight(.medium))
                .labelStyle(.titleAndIcon)
        }
        .menuStyle(.button)
        .buttonStyle(.bordered)
        .buttonBorderShape(.capsule)
        .controlSize(.small)
    }

    private func summaryChip(_ text: String, symbol: String, color: Color) -> some View {
        Label(text, systemImage: symbol)
            .font(.caption.weight(.medium))
            .foregroundStyle(color == .secondary ? Color.secondary : color)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(color == .secondary ? Color.gray.opacity(0.12) : color.opacity(0.14),
                        in: .capsule)
    }

    private func delete(_ item: StockItem) {
        if let product = item.product { context.delete(product) }
        context.delete(item)
        try? context.save()
        Feedback.shared.play(.removed)
    }
}
