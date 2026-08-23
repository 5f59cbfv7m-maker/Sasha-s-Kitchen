import SwiftData
import SwiftUI

/// Новый продукт в каталог и сразу в холодильник.
struct AddProductView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.modelContext) private var context
    @Query private var products: [Product]

    @State private var name = ""
    @State private var category = "Овощи"
    @State private var customCategory = ""
    @State private var unit = "г"
    @State private var gramsPerUnit: Double = 100
    @State private var calories: Double = 0
    @State private var protein: Double = 0
    @State private var fat: Double = 0
    @State private var carbs: Double = 0
    @State private var initialAmount: Double = 0
    @State private var lowThreshold: Double = 0
    @State private var shelfLifeEnabled = false
    @State private var shelfLifeDays: Int = 5

    private static let units = ["г", "мл", "шт"]
    private static let newCategoryTag = "__new__"

    private var trimmedName: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }

    private var existing: Product? {
        products.first { $0.name.caseInsensitiveCompare(trimmedName) == .orderedSame }
    }

    private var categories: [String] {
        Array(Set(products.map(\.category) + CategoryStyle.order)).sorted()
    }

    private var resolvedCategory: String {
        category == Self.newCategoryTag
            ? customCategory.trimmingCharacters(in: .whitespaces)
            : category
    }

    private var canSave: Bool {
        !trimmedName.isEmpty && !resolvedCategory.isEmpty && existing == nil
    }

    /// КБЖУ на 100 г, применённые к введённому количеству — видно сразу, не после сохранения.
    private var preview: Nutrition {
        let grams = initialAmount * (unit == "шт" ? gramsPerUnit : 1)
        return Nutrition.from(
            per100: Nutrition(calories: calories, protein: protein, fat: fat, carbs: carbs),
            grams: grams)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Название", text: $name)
                    Picker("Категория", selection: $category) {
                        ForEach(categories, id: \.self) { Text($0).tag($0) }
                        Divider()
                        Text("Новая категория…").tag(Self.newCategoryTag)
                    }
                    if category == Self.newCategoryTag {
                        TextField("Название категории", text: $customCategory)
                    }
                    if let existing {
                        Label("«\(existing.name)» уже есть в каталоге — пополните остаток на вкладке «Холодильник».",
                              systemImage: "exclamationmark.triangle.fill")
                            .font(.footnote)
                            .foregroundStyle(.orange)
                    }
                } header: {
                    Text("Продукт")
                }

                Section("Единица измерения") {
                    Picker("Единица", selection: $unit) {
                        ForEach(Self.units, id: \.self) { Text($0).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    if unit == "шт" {
                        numberField("Вес одной штуки", value: $gramsPerUnit, unit: "г")
                    }
                }

                Section("КБЖУ на 100 г") {
                    numberField("Калорийность", value: $calories, unit: "ккал")
                    numberField("Белки", value: $protein, unit: "г")
                    numberField("Жиры", value: $fat, unit: "г")
                    numberField("Углеводы", value: $carbs, unit: "г")
                }

                Section {
                    numberField("Сколько кладём", value: $initialAmount, unit: unit)
                    numberField("Считать «на исходе» ниже", value: $lowThreshold, unit: unit)
                    Toggle("Есть срок годности", isOn: $shelfLifeEnabled.animation())
                    if shelfLifeEnabled {
                        Stepper("Годен \(shelfLifeDays) дн. с покупки",
                                value: $shelfLifeDays, in: 1...365)
                    }
                } header: {
                    Text("В холодильник")
                } footer: {
                    if initialAmount > 0 && calories > 0 {
                        Text("В этом количестве — \(Fmt.kcal(preview.calories)).")
                    }
                }

                Section {
                    // Слот под сканер: сам сканер и запрос к Open Food Facts — фаза 2,
                    // но место в интерфейсе и разрешение на камеру заложены сейчас.
                    Button {
                    } label: {
                        Label("Сканировать штрихкод", systemImage: "barcode.viewfinder")
                    }
                    .disabled(true)
                } footer: {
                    Text("Сканер появится во второй фазе — он подставит название и КБЖУ из базы Open Food Facts. Доступ к камере можно выдать заранее на вкладке «Настройки».")
                }
            }
            .navigationTitle("Новый продукт")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Добавить") { save() }.disabled(!canSave)
                }
            }
        }
    }

    private func numberField(_ title: String, value: Binding<Double>, unit: String) -> some View {
        HStack {
            Text(title)
            Spacer()
            TextField("0", value: value, format: Fmt.amountStyle)
                .keyboardType(.decimalPad)
                .multilineTextAlignment(.trailing)
                .monospacedDigit()
                .frame(maxWidth: 110)
            Text(unit)
                .foregroundStyle(.secondary)
                .frame(width: 40, alignment: .leading)
        }
    }

    private func save() {
        let product = Product(
            name: trimmedName, category: resolvedCategory, unit: unit,
            gramsPerUnit: unit == "шт" ? max(1, gramsPerUnit) : 1,
            caloriesPer100: calories, proteinPer100: protein,
            fatPer100: fat, carbsPer100: carbs,
            lowStockThreshold: lowThreshold,
            shelfLifeDays: shelfLifeEnabled ? shelfLifeDays : nil,
            isCustom: true)
        context.insert(product)

        let expiry = shelfLifeEnabled
            ? Date.now.addingTimeInterval(Double(shelfLifeDays) * 86_400) : nil
        context.insert(StockItem(product: product, currentAmount: initialAmount,
                                 initialAmount: initialAmount, expiryDate: expiry))
        try? context.save()
        Feedback.shared.play(.added)
        dismiss()
    }
}
