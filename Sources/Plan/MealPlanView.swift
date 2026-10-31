import SwiftData
import SwiftUI

/// Меню на неделю: по дням — что готовим. Одна кнопка кладёт в список
/// покупок всё, чего на эти блюда не хватит, с учётом того, что уже есть.
struct MealPlanView: View {
    @Environment(\.modelContext) private var context
    @Query(sort: \PlannedMeal.createdAt) private var meals: [PlannedMeal]
    @Query private var stock: [StockItem]

    @State private var pickingDay: Date?
    @State private var message: String?

    private var days: [Date] {
        let today = Calendar.current.startOfDay(for: .now)
        return (0..<7).compactMap { Calendar.current.date(byAdding: .day, value: $0, to: today) }
    }

    private var weekMeals: [PlannedMeal] {
        guard let first = days.first else { return [] }
        return meals.filter { $0.day >= first }
    }

    private var shortages: [Shortage] {
        MealPlan.shortages(for: weekMeals, stock: Kitchen.snapshot(of: stock))
    }

    var body: some View {
        NavigationStack {
            List {
                if weekMeals.isEmpty {
                    Section {
                        Text("Добавьте рецепты на неделю — приложение посчитает, чего не хватит, и положит это в список покупок.")
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Section {
                        Button {
                            addMissing()
                        } label: {
                            Label(shortages.isEmpty ? "Для меню всё есть" : "Недостающее — в список покупок",
                                  systemImage: shortages.isEmpty ? "checkmark.circle" : "cart.badge.plus")
                        }
                        .disabled(shortages.isEmpty)
                    } footer: {
                        if !shortages.isEmpty {
                            Text(shortages.map(\.productName).formatted(.list(type: .and)))
                        }
                    }
                }
                ForEach(days, id: \.self) { day in
                    Section {
                        ForEach(meals.filter { Calendar.current.isDate($0.day, inSameDayAs: day) }) { row($0) }
                            .onDelete { offsets in delete(offsets, on: day) }
                        Button {
                            pickingDay = day
                        } label: {
                            Label("Добавить блюдо", systemImage: "plus")
                        }
                    } header: {
                        Text(title(for: day))
                    }
                }
            }
            .navigationTitle("Меню")
            .sheet(item: $pickingDay) { day in
                RecipeChooser { recipe in
                    MealPlan.add(recipe, on: day, servings: recipe.baseServings, in: context)
                    Feedback.shared.play(.added)
                }
            }
            .alert("Готово", isPresented: Binding { message != nil } set: { if !$0 { message = nil } }) {
                Button("Понятно") {}
            } message: {
                Text(message ?? "")
            }
        }
    }

    private func row(_ meal: PlannedMeal) -> some View {
        NavigationLink {
            if let recipe = meal.recipe { RecipeDetailView(recipe: recipe) }
        } label: {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(meal.recipe?.name ?? "—")
                        .strikethrough(meal.isCooked)
                        .foregroundStyle(meal.isCooked ? .secondary : .primary)
                    Text(status(of: meal))
                        .font(.caption)
                        .foregroundStyle(meal.isCooked ? Color.secondary : isReady(meal) ? .green : .orange)
                }
                Spacer()
                Stepper("\(meal.servings)", value: Binding(get: { meal.servings },
                                                         set: { meal.servings = max(1, $0); try? context.save() }),
                        in: 1...20)
                    .labelsHidden()
                    .fixedSize()
                Text("\(meal.servings)").monospacedDigit().foregroundStyle(.secondary)
            }
        }
    }

    private func isReady(_ meal: PlannedMeal) -> Bool {
        guard let recipe = meal.recipe else { return false }
        return Kitchen.status(for: recipe, servings: meal.servings, stock: Kitchen.snapshot(of: stock)).isReady
    }

    private func status(of meal: PlannedMeal) -> String {
        if meal.isCooked { return String(localized: "Приготовлено") }
        return isReady(meal) ? String(localized: "Все ингредиенты есть") : String(localized: "Чего-то не хватает")
    }

    private func title(for day: Date) -> String {
        if Calendar.current.isDateInToday(day) { return String(localized: "Сегодня") }
        if Calendar.current.isDateInTomorrow(day) { return String(localized: "Завтра") }
        return day.formatted(.dateTime.weekday(.wide).day().month(.wide))
    }

    private func delete(_ offsets: IndexSet, on day: Date) {
        let dayMeals = meals.filter { Calendar.current.isDate($0.day, inSameDayAs: day) }
        for index in offsets { context.delete(dayMeals[index]) }
        try? context.save()
    }

    private func addMissing() {
        let missing = shortages
        Kitchen.addMissing(missing, in: context)
        Feedback.shared.play(.added)
        message = String(localized: "В список покупок добавлено: \(missing.count)")
    }
}

/// Выбор рецепта для меню: поиск по названию, готовые к готовке — сверху.
private struct RecipeChooser: View {
    @Environment(\.dismiss) private var dismiss
    @Query(sort: \Recipe.name) private var recipes: [Recipe]
    @State private var search = ""
    let onPick: (Recipe) -> Void

    var body: some View {
        NavigationStack {
            List(recipes.filter { search.isEmpty || $0.name.localizedStandardContains(search) }) { recipe in
                Button {
                    onPick(recipe)
                    dismiss()
                } label: {
                    Text(recipe.name).foregroundStyle(.primary)
                }
            }
            .searchable(text: $search, prompt: "Рецепт или ингредиент")
            .navigationTitle("Выберите рецепт")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Отмена") { dismiss() } }
            }
        }
    }
}

extension Date: @retroactive Identifiable {
    public var id: TimeInterval { timeIntervalSinceReferenceDate }
}
