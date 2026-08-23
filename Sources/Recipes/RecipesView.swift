import SwiftData
import SwiftUI

/// Что можно приготовить прямо сейчас — и всё остальное.
struct RecipesView: View {
    @Environment(\.modelContext) private var context
    @Query private var recipes: [Recipe]
    @Query private var stock: [StockItem]

    @State private var search = ""
    @State private var onlyAvailable = false
    @State private var scope: Scope = .all
    @State private var isCreating = false

    enum Scope: String, CaseIterable, Identifiable {
        case all = "Все"
        case custom = "Мои"
        var id: String { rawValue }
    }

    private var snapshot: StockSnapshot { Kitchen.snapshot(of: stock) }

    private var visible: [(recipe: Recipe, status: RecipeAvailability.Status)] {
        let query = search.trimmingCharacters(in: .whitespaces)
        let snapshot = snapshot
        return recipes
            .filter { scope == .all || $0.isCustom }
            .filter { query.isEmpty || $0.name.localizedCaseInsensitiveContains(query)
                || $0.orderedIngredients.contains { $0.productName.localizedCaseInsensitiveContains(query) } }
            .map { (recipe: $0, status: Kitchen.status(for: $0, servings: $0.baseServings, stock: snapshot)) }
            .filter { !onlyAvailable || $0.status.isReady }
            // Готовые к готовке — вперёд, дальше по алфавиту.
            .sorted {
                if $0.status.isReady != $1.status.isReady { return $0.status.isReady }
                return $0.recipe.name.localizedCaseInsensitiveCompare($1.recipe.name) == .orderedAscending
            }
    }

    private var readyCount: Int {
        let snapshot = snapshot
        return recipes.filter {
            Kitchen.status(for: $0, servings: $0.baseServings, stock: snapshot).isReady
        }.count
    }

    private let columns = [GridItem(.adaptive(minimum: 240, maximum: 380), spacing: 16)]

    var body: some View {
        NavigationStack {
            ScrollView {
                filterBar
                LazyVGrid(columns: columns, spacing: 16) {
                    ForEach(visible, id: \.recipe.persistentModelID) { entry in
                        NavigationLink {
                            RecipeDetailView(recipe: entry.recipe)
                        } label: {
                            RecipeCard(recipe: entry.recipe, status: entry.status,
                                       servings: entry.recipe.baseServings)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal)
                .padding(.bottom, 40)

                if visible.isEmpty {
                    EmptyStateView(
                        symbol: onlyAvailable ? "cart.badge.questionmark" : "fork.knife",
                        title: onlyAvailable ? "Пока не из чего готовить" : "Рецептов нет",
                        message: onlyAvailable
                            ? "Ни один рецепт не собирается из того, что есть. Снимите фильтр, чтобы увидеть, чего не хватает."
                            : "Создайте свой рецепт кнопкой «плюс».")
                }
            }
            .navigationTitle("Рецепты")
            .searchable(text: $search, prompt: "Рецепт или ингредиент")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { isCreating = true } label: {
                        Label("Создать рецепт", systemImage: "plus")
                    }
                }
            }
            .sheet(isPresented: $isCreating) { RecipeEditorView() }
        }
    }

    private var filterBar: some View {
        HStack(spacing: 12) {
            Toggle(isOn: $onlyAvailable.animation()) {
                Label("Могу приготовить сейчас", systemImage: "checkmark.circle")
                    .font(.subheadline)
            }
            .toggleStyle(.button)
            .buttonStyle(.bordered)
            .buttonBorderShape(.capsule)
            .tint(onlyAvailable ? .green : .secondary)

            Picker("Показывать", selection: $scope) {
                ForEach(Scope.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .frame(maxWidth: 200)

            Spacer()

            Text("\(readyCount) из \(recipes.count) готовы к готовке")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal)
        .padding(.bottom, 12)
    }
}
