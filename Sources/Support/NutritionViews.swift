import SwiftUI

/// Один макрос в виде плитки. Цвета одни и те же во всём приложении, чтобы
/// «синее — белки» читалось без подписи.
struct MacroTile: View {
    let title: String
    let grams: Double
    let color: Color

    var body: some View {
        VStack(spacing: 2) {
            Text("\(Fmt.number(grams)) г")
                .font(.subheadline.weight(.semibold))
                .monospacedDigit()
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 8)
        .background(color.opacity(0.14), in: .rect(cornerRadius: 10))
    }
}

struct NutritionRow: View {
    let nutrition: Nutrition
    var caloriesCaption: String = "ккал"

    var body: some View {
        HStack(spacing: 8) {
            VStack(spacing: 2) {
                Text(Fmt.integer(Int(nutrition.calories.rounded())))
                    .font(.title3.weight(.bold))
                    .monospacedDigit()
                Text(caloriesCaption)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
            .background(Color.accentColor.opacity(0.16), in: .rect(cornerRadius: 10))

            MacroTile(title: "белки", grams: nutrition.protein, color: .blue)
            MacroTile(title: "жиры", grams: nutrition.fat, color: .orange)
            MacroTile(title: "углеводы", grams: nutrition.carbs, color: .green)
        }
    }
}

/// Пустой экран с иконкой и подсказкой — используется на всех вкладках.
struct EmptyStateView: View {
    let symbol: String
    let title: String
    let message: String

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 46))
                .foregroundStyle(.tertiary)
            Text(title).font(.title3.weight(.semibold))
            Text(message)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 420)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 60)
    }
}
