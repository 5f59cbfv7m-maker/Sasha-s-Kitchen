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
    @State private var barcode: String?
    @State private var isScanning = false

    /// Заполненная форма — после сканера: название, КБЖУ и штрихкод из Open Food Facts.
    init(prefill: FoodFacts.Info? = nil) {
        guard let prefill else { return }
        _barcode = State(initialValue: prefill.barcode)
        _name = State(initialValue: prefill.name)
        if let per100 = prefill.per100 {
            _calories = State(initialValue: per100.calories)
            _protein = State(initialValue: per100.protein)
            _fat = State(initialValue: per100.fat)
            _carbs = State(initialValue: per100.carbs)
        }
    }

    private static let newCategoryTag = "__new__"

    private var trimmedName: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }

    private var existing: Product? {
        products.first { $0.name.sameName(as: trimmedName) }
    }

    private var categories: [String] {
        Array(Set(products.map(\.category) + CategoryStyle.order))
            .sorted { Fmt.category($0).localizedStandardCompare(Fmt.category($1)) == .orderedAscending }
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
        let grams = initialAmount * (unit == UnitCode.piece ? gramsPerUnit : 1)
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
                        ForEach(categories, id: \.self) { Text(Fmt.category($0)).tag($0) }
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
                        ForEach(UnitCode.all, id: \.self) { Text(Fmt.inputUnit($0).label).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    if unit == UnitCode.piece {
                        numberField("Вес одной штуки", value: $gramsPerUnit, unit: UnitCode.gram)
                    }
                }

                Section("КБЖУ на 100 г") {
                    numberField("Калорийность", value: $calories, unit: "ккал", converts: false)
                    numberField("Белки", value: $protein, unit: UnitCode.gram, converts: false)
                    numberField("Жиры", value: $fat, unit: UnitCode.gram, converts: false)
                    numberField("Углеводы", value: $carbs, unit: UnitCode.gram, converts: false)
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
                    Button {
                        isScanning = true
                    } label: {
                        Label(barcode == nil ? "Сканировать штрихкод" : "Штрихкод: \(barcode ?? "")",
                              systemImage: "barcode.viewfinder")
                    }
                } footer: {
                    Text("Сканер подставит название и КБЖУ из открытой базы Open Food Facts, а штрихкод запомнится — в следующий раз продукт узнается сразу.")
                }
            }
            .sheet(isPresented: $isScanning) {
                ScanSheet { info in
                    barcode = info.barcode
                    if !info.name.isEmpty { name = info.name }
                    if let per100 = info.per100 {
                        calories = per100.calories
                        protein = per100.protein
                        fat = per100.fat
                        carbs = per100.carbs
                    }
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

    private func numberField(_ title: LocalizedStringKey, value: Binding<Double>,
                             unit: String, converts: Bool = true) -> some View {
        HStack {
            Text(title)
            Spacer()
            AmountField(amount: value, unit: unit, width: 110, converts: converts)
        }
    }

    private func save() {
        let product = Product(
            name: trimmedName, category: resolvedCategory, unit: unit,
            gramsPerUnit: unit == UnitCode.piece ? max(1, gramsPerUnit) : 1,
            caloriesPer100: calories, proteinPer100: protein,
            fatPer100: fat, carbsPer100: carbs,
            lowStockThreshold: lowThreshold,
            shelfLifeDays: shelfLifeEnabled ? shelfLifeDays : nil,
            barcode: barcode, isCustom: true)
        context.insert(product)

        let expiry = shelfLifeEnabled ? Kitchen.expiry(afterDays: shelfLifeDays) : nil
        context.insert(StockItem(product: product, currentAmount: initialAmount,
                                 initialAmount: initialAmount, expiryDate: expiry))
        try? context.save()
        Feedback.shared.play(.added)
        dismiss()
    }
}
