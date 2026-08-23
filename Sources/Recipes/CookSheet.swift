import SwiftData
import SwiftUI

/// Подтверждение готовки: по умолчанию списываем расчётные граммовки, но
/// пользователь правит их под то, что реально ушло в кастрюлю.
struct CookSheet: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]

    let recipe: Recipe
    let servings: Int
    var onCooked: () -> Void = {}

    @State private var amounts: [String: Double] = [:]

    private var requirements: [IngredientRequirement] {
        Kitchen.requirements(for: recipe, servings: servings)
    }

    private var snapshot: StockSnapshot { Kitchen.snapshot(of: stock) }

    /// КБЖУ считаем по фактическим числам, а не по рецептурным.
    private var actualNutrition: Nutrition {
        recipe.orderedIngredients.map { ingredient -> Nutrition in
            guard let product = ingredient.product,
                  let amount = amounts[product.name] else { return .zero }
            return product.nutrition(forAmount: min(amount, snapshot.available(product.name)))
        }.total
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(requirements) { requirement in
                        row(requirement)
                    }
                } header: {
                    Text("Фактически использовано")
                } footer: {
                    Text("По умолчанию — расчёт на \(servings) порц. Поправьте, если положили больше или меньше: спишется ровно то, что здесь указано.")
                }

                Section("Получилось") {
                    NutritionRow(nutrition: actualNutrition)
                        .listRowInsets(EdgeInsets(top: 8, leading: 12, bottom: 8, trailing: 12))
                    LabeledContent("На порцию",
                                   value: Fmt.kcal(actualNutrition.calories / Double(max(1, servings))))
                }
            }
            .navigationTitle("Приготовил")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Списать") { cook() }
                        .fontWeight(.semibold)
                }
            }
        }
        .onAppear {
            guard amounts.isEmpty else { return }
            for requirement in requirements {
                amounts[requirement.productName] = (requirement.amount * 10).rounded() / 10
            }
        }
    }

    private func row(_ requirement: IngredientRequirement) -> some View {
        let have = snapshot.available(requirement.productName)
        let planned = amounts[requirement.productName] ?? requirement.amount
        let short = planned > have + 0.01
        return VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(requirement.productName)
                Spacer()
                TextField("0", value: Binding(
                    get: { amounts[requirement.productName] ?? requirement.amount },
                    set: { amounts[requirement.productName] = max(0, $0) }),
                          format: Fmt.amountStyle)
                    .keyboardType(.decimalPad)
                    .multilineTextAlignment(.trailing)
                    .monospacedDigit()
                    .frame(maxWidth: 90)
                Text(requirement.unit)
                    .foregroundStyle(.secondary)
                    .frame(width: 30, alignment: .leading)
            }
            Text(short
                 ? "в холодильнике только \(Fmt.amount(have, unit: requirement.unit)) — спишется этот остаток"
                 : "останется \(Fmt.amount(have - planned, unit: requirement.unit))")
                .font(.caption)
                .foregroundStyle(short ? .orange : .secondary)
        }
        .padding(.vertical, 2)
    }

    private func cook() {
        Kitchen.cook(recipe: recipe, servings: servings, amounts: amounts, in: context)
        Feedback.shared.play(.cooked)
        onCooked()
        dismiss()
    }
}
