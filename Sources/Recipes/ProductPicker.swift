import SwiftData
import SwiftUI

/// Выбор продукта из каталога — общий для редактора рецептов.
struct ProductPicker: View {
    @Environment(\.dismiss) private var dismiss
    @Query(sort: \Product.name) private var products: [Product]

    var exclude: Set<String> = []
    let onPick: (Product) -> Void

    @State private var search = ""

    private var grouped: [(category: String, items: [Product])] {
        let query = search.trimmingCharacters(in: .whitespaces)
        let filtered = products.filter {
            !exclude.contains($0.name)
                && (query.isEmpty || $0.name.localizedCaseInsensitiveContains(query))
        }
        return Dictionary(grouping: filtered, by: \.category)
            .map { (category: $0.key, items: $0.value.sorted { $0.name < $1.name }) }
            .sorted { CategoryStyle.rank($0.category) < CategoryStyle.rank($1.category) }
    }

    var body: some View {
        NavigationStack {
            List {
                ForEach(grouped, id: \.category) { group in
                    Section(group.category) {
                        ForEach(group.items) { product in
                            Button {
                                onPick(product)
                                dismiss()
                            } label: {
                                HStack {
                                    Image(systemName: CategoryStyle.symbol(for: product.category))
                                        .foregroundStyle(CategoryStyle.color(for: product.category))
                                        .frame(width: 24)
                                    VStack(alignment: .leading, spacing: 1) {
                                        Text(product.name).foregroundStyle(.primary)
                                        Text("\(Fmt.kcal(product.caloriesPer100)) / 100 г · \(product.unit)")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }
                }
            }
            .searchable(text: $search, prompt: "Найти продукт")
            .navigationTitle("Ингредиент")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
            }
        }
    }
}
