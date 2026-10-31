import SwiftData
import SwiftUI

/// Главный экран: что лежит в холодильнике и сколько осталось.
struct FridgeView: View {
    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]

    @State private var search = ""
    @State private var sortMode: SortMode = .category
    @State private var isAddingProduct = false
    @State private var isScanning = false
    @State private var adjusting: StockItem?
    @State private var deleting: StockItem?

    enum SortMode: String, CaseIterable, Identifiable {
        case category, running, expiry, name

        var id: String { rawValue }
        var title: String {
            switch self {
            case .category: String(localized: "По категориям")
            case .running: String(localized: "Сначала заканчивается")
            case .expiry: String(localized: "По сроку годности")
            case .name: String(localized: "По названию")
            }
        }
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
            return item.name.localizedStandardContains(query)
                || Fmt.category(item.category).localizedStandardContains(query)
        }
    }

    private var sorted: [StockItem] {
        switch sortMode {
        case .category:
            items.sorted {
                let (l, r) = (CategoryStyle.rank($0.category), CategoryStyle.rank($1.category))
                if l != r { return l < r }
                return $0.name.localizedStandardCompare($1.name) == .orderedAscending
            }
        case .running:
            items.sorted { $0.progress < $1.progress }
        case .expiry:
            items.sorted {
                ($0.daysUntilExpiry ?? .max, $0.name) < ($1.daysUntilExpiry ?? .max, $1.name)
            }
        case .name:
            items.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
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
                if !stock.isEmpty { summaryBar }
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

                if stock.isEmpty {
                    firstLaunch
                } else if items.isEmpty {
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
                    .keyboardShortcut("n", modifiers: .command)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        isScanning = true
                    } label: {
                        Label("Сканировать штрихкод", systemImage: "barcode.viewfinder")
                    }
                }
            }
            .sheet(isPresented: $isAddingProduct) { AddProductView() }
            .sheet(isPresented: $isScanning) { ScanSheet() }
            .sheet(item: $adjusting) { item in AdjustStockView(item: item) }
            .confirmationDialog(deleteTitle, isPresented: isDeleting, titleVisibility: .visible,
                                presenting: deleting) { item in
                Button("Удалить", role: .destructive) { delete(item) }
            } message: { item in
                let uses = item.product?.recipeUses?.count ?? 0
                if uses > 0 {
                    Text("Продукт пропадёт и из рецептов, где он есть: \(uses).")
                }
            }
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
                        deleting = item
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
            Text(Fmt.category(category)).font(.headline)
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
        // Чипы листаются вбок: на iPhone и в длинных переводах они не влезают в строку.
        return HStack(spacing: 10) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 10) {
                    summaryChip("\(total.count) продуктов", symbol: "shippingbox", color: .secondary)
                    if low > 0 { summaryChip("\(low) на исходе", symbol: "exclamationmark.circle", color: .orange) }
                    if empty > 0 { summaryChip("\(empty) закончилось", symbol: "xmark.circle", color: .red) }
                    if expiring > 0 { summaryChip("\(expiring) скоро испортится", symbol: "clock", color: .yellow) }
                }
            }
            sortMenu
        }
        .padding(.horizontal)
        .padding(.bottom, 10)
    }

    private var sortMenu: some View {
        Menu {
            Picker("Сортировка", selection: $sortMode) {
                ForEach(SortMode.allCases) { mode in
                    Label(mode.title, systemImage: mode.symbol).tag(mode)
                }
            }
        } label: {
            Label(sortMode.title, systemImage: "arrow.up.arrow.down")
                .font(.caption.weight(.medium))
                .labelStyle(.titleAndIcon)
        }
        .menuStyle(.button)
        .buttonStyle(.bordered)
        .buttonBorderShape(.capsule)
        .controlSize(.small)
    }

    private func summaryChip(_ text: LocalizedStringKey, symbol: String, color: Color) -> some View {
        Label(text, systemImage: symbol)
            .font(.caption.weight(.medium))
            .foregroundStyle(color == .secondary ? Color.secondary : color)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(color == .secondary ? Color.gray.opacity(0.12) : color.opacity(0.14),
                        in: .capsule)
    }

    private var deleteTitle: String {
        String(localized: "Удалить «\(deleting?.name ?? "")»?")
    }

    private var isDeleting: Binding<Bool> {
        Binding { deleting != nil } set: { if !$0 { deleting = nil } }
    }

    private var starterSet: Binding<SeedSet> {
        Binding { SeedSet.current } set: { UserDefaults.standard.set($0.rawValue, forKey: SeedSet.key) }
    }

    /// Первый запуск: база пуста. С iCloud данные с других устройств придут
    /// сами, поэтому стартовый набор — по кнопке, а не молча.
    @ViewBuilder
    private var firstLaunch: some View {
        // Семейная кухня приходит из iCloud владельца — стартовый набор в неё
        // не грузим, он ушёл бы владельцу.
        if FamilyKitchen.shared.joined[UserDefaults.standard.string(forKey: FamilyKitchen.activeKey) ?? ""] != nil {
            EmptyStateView(symbol: "icloud.and.arrow.down", title: "Кухня загружается",
                           message: "Продукты и рецепты семейной кухни придут из iCloud через минуту.")
        } else {
            starterSetOffer
        }
    }

    private var starterSetOffer: some View {
        VStack(spacing: 18) {
            EmptyStateView(
                symbol: "refrigerator",
                title: "Холодильник пуст",
                message: "Добавьте первый продукт кнопкой «плюс» или начните со стартового набора продуктов и рецептов.")
            // Набор — по региону устройства, но его можно сменить прямо здесь.
            Picker("Стартовый набор", selection: starterSet) {
                ForEach(SeedSet.allCases, id: \.self) { Text($0.title).tag($0) }
            }
            .pickerStyle(.menu)
            Button {
                SeedLoader.loadIfNeeded(into: context)
                Feedback.shared.play(.cooked)
            } label: {
                Label("Загрузить стартовый набор", systemImage: "sparkles")
                    .padding(.horizontal, 8)
            }
            .buttonStyle(.borderedProminent)
            if FileManager.default.ubiquityIdentityToken != nil {
                Label("Уже пользуетесь приложением на другом устройстве? Подождите минуту — продукты и рецепты придут из iCloud.",
                      systemImage: "icloud")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 420)
            }
        }
        .padding(.horizontal)
    }

    private func delete(_ item: StockItem) {
        if let product = item.product { context.delete(product) }
        context.delete(item)
        try? context.save()
        Feedback.shared.play(.removed)
    }
}
