import SwiftData
import SwiftUI

/// Карточка рецепта: граммовки под выбранное число порций, итоговая КБЖУ
/// и кнопка «Приготовил», которая списывает продукты с остатков.
struct RecipeDetailView: View {
    @Environment(\.modelContext) private var context
    @Environment(\.dismiss) private var dismiss
    @Query private var stock: [StockItem]

    let recipe: Recipe

    @State private var servings: Int = 2
    @State private var isCooking = false
    @State private var isEditing = false
    @State private var isConfirmingDelete = false
    @State private var banner: LocalizedStringKey?

    private var snapshot: StockSnapshot { Kitchen.snapshot(of: stock) }
    private var requirements: [IngredientRequirement] {
        Kitchen.requirements(for: recipe, servings: servings)
    }
    private var status: RecipeAvailability.Status {
        RecipeAvailability.status(for: requirements, stock: snapshot)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                header
                servingsPicker
                ingredientsSection
                nutritionSection
                if let steps = recipe.steps, !steps.isEmpty {
                    section("Приготовление") {
                        Text(steps)
                            .font(.body)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                cookButton
            }
            .padding()
            .frame(maxWidth: 760)
            .frame(maxWidth: .infinity)
        }
        .navigationTitle(recipe.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if recipe.isCustom {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button { isEditing = true } label: {
                            Label("Изменить", systemImage: "pencil")
                        }
                        Button(role: .destructive) {
                            isConfirmingDelete = true
                        } label: {
                            Label("Удалить рецепт", systemImage: "trash")
                        }
                    } label: {
                        Label("Ещё", systemImage: "ellipsis.circle")
                    }
                }
            }
        }
        .sheet(isPresented: $isEditing) { RecipeEditorView(editing: recipe) }
        .sheet(isPresented: $isCooking) {
            CookSheet(recipe: recipe, servings: servings) { showBanner("Списано с остатков") }
        }
        .confirmationDialog("Удалить рецепт «\(recipe.name)»?", isPresented: $isConfirmingDelete,
                            titleVisibility: .visible) {
            Button("Удалить", role: .destructive) {
                context.delete(recipe)
                try? context.save()
                dismiss()
            }
        } message: {
            Text("Записи в журнале останутся.")
        }
        .overlay(alignment: .top) { cookedBanner }
        .onAppear { servings = recipe.baseServings }
    }

    // MARK: Шапка

    private var header: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let data = recipe.photoData, let image = UIImage(data: data) {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFill()
                    .frame(height: 220)
                    .frame(maxWidth: .infinity)
                    .clipShape(.rect(cornerRadius: 18))
            }
            // На iPhone значок «всё есть» уходит на свою строку.
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 14) {
                    facts
                    Spacer()
                    statusChip
                }
                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 14) { facts }
                    statusChip
                }
            }
            .font(.subheadline)
            .foregroundStyle(.secondary)
        }
    }

    @ViewBuilder
    private var facts: some View {
        Label(Fmt.minutes(recipe.cookingTimeMinutes), systemImage: "clock")
        Label("базово \(recipe.baseServings) порц.", systemImage: "person.2")
    }

    @ViewBuilder
    private var statusChip: some View {
        switch status {
        case .ready:
            Label("Все ингредиенты есть", systemImage: "checkmark.circle.fill")
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.green)
        case .missing(let shortages):
            Label("Не хватает \(shortages.count)", systemImage: "exclamationmark.triangle.fill")
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.orange)
        }
    }

    // MARK: Порции

    private var servingsPicker: some View {
        section("Порции") {
            HStack {
                // Слово «порции» уже в заголовке секции — здесь только число.
                Stepper(value: $servings, in: 1...20) {
                    Text(verbatim: "\(servings)")
                        .font(.title2.weight(.bold))
                        .monospacedDigit()
                        .contentTransition(.numericText())
                }
                .accessibilityLabel(Text("Порций: \(servings)"))
                Spacer()
                if servings != recipe.baseServings {
                    Button("Сбросить") { withAnimation { servings = recipe.baseServings } }
                        .font(.subheadline)
                }
            }
            .animation(.snappy, value: servings)
        }
    }

    // MARK: Ингредиенты

    private var ingredientsSection: some View {
        section("Ингредиенты") {
            VStack(spacing: 0) {
                ForEach(Array(requirements.enumerated()), id: \.element.id) { index, requirement in
                    if index > 0 { Divider() }
                    ingredientRow(requirement)
                }
            }
        }
    }

    private func ingredientRow(_ requirement: IngredientRequirement) -> some View {
        let have = snapshot.available(requirement.productName)
        let enough = have + 0.01 >= requirement.amount
        let product = recipe.orderedIngredients
            .first { $0.productName == requirement.productName }?.product
        return HStack(spacing: 12) {
            Image(systemName: enough ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                .foregroundStyle(enough ? .green : .orange)
            VStack(alignment: .leading, spacing: 2) {
                Text(requirement.productName)
                Text(enough
                     ? "в холодильнике \(Fmt.amount(have, unit: requirement.unit))"
                     : "не хватает \(Fmt.amount(requirement.amount - have, unit: requirement.unit))")
                    .font(.caption)
                    .foregroundStyle(enough ? Color.secondary : Color.orange)
            }
            Spacer()
            Text(product?.amountLabel(requirement.amount)
                 ?? Fmt.amount(requirement.amount, unit: requirement.unit))
                .font(.body.weight(.semibold))
                .monospacedDigit()
                .contentTransition(.numericText())
        }
        .padding(.vertical, 9)
    }

    // MARK: КБЖУ

    private var nutritionSection: some View {
        let perServing = recipe.nutritionPerServing(for: servings)
        let total = recipe.nutrition(for: servings)
        return section("КБЖУ") {
            VStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("На порцию").font(.caption).foregroundStyle(.secondary)
                    NutritionRow(nutrition: perServing)
                }
                VStack(alignment: .leading, spacing: 6) {
                    Text("Всё блюдо").font(.caption).foregroundStyle(.secondary)
                    NutritionRow(nutrition: total)
                }
            }
        }
    }

    // MARK: Готовка

    private var cookButton: some View {
        VStack(spacing: 8) {
            Button {
                if status.isReady {
                    isCooking = true
                } else {
                    Feedback.shared.play(.warning)
                    isCooking = true
                }
            } label: {
                Label("Приготовил", systemImage: "flame.fill")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
            }
            .buttonStyle(.borderedProminent)
            .tint(status.isReady ? .accentColor : .orange)

            if case .missing(let shortages) = status {
                Text("Не хватает: \(shortages.map { "\($0.productName) (\(Fmt.amount($0.missing, unit: $0.unit)))" }.formatted(.list(type: .and)))")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .multilineTextAlignment(.center)
                Button {
                    Kitchen.addMissing(shortages, in: context)
                    Feedback.shared.play(.added)
                    showBanner("Недостающее — в списке покупок")
                } label: {
                    Label("Добавить недостающее в покупки", systemImage: "cart.badge.plus")
                }
                .buttonStyle(.bordered)
            }
        }
    }

    @ViewBuilder
    private var cookedBanner: some View {
        if let banner {
            Label(banner, systemImage: "checkmark.circle.fill")
                .font(.subheadline.weight(.semibold))
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .background(.regularMaterial, in: .capsule)
                .foregroundStyle(.green)
                .padding(.top, 8)
                .transition(.move(edge: .top).combined(with: .opacity))
                .task {
                    try? await Task.sleep(for: .seconds(2))
                    withAnimation { self.banner = nil }
                }
        }
    }

    // MARK: Каркас секции

    private func showBanner(_ text: LocalizedStringKey) {
        withAnimation { banner = text }
    }

    private func section<Content: View>(_ title: LocalizedStringKey,
                                        @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title)
                .font(.headline)
            content()
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.background.secondary, in: .rect(cornerRadius: 16))
        }
    }
}
