import PhotosUI
import SwiftData
import SwiftUI

/// Создание и правка собственного рецепта. КБЖУ пересчитывается на лету,
/// по мере добавления ингредиентов.
struct RecipeEditorView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.modelContext) private var context

    var editing: Recipe?

    @State private var name = ""
    @State private var minutes = 20
    @State private var servings = 2
    @State private var steps = ""
    @State private var drafts: [Draft] = []
    @State private var photoData: Data?
    @State private var photoItem: PhotosPickerItem?
    @State private var isPickingProduct = false

    struct Draft: Identifiable {
        let id = UUID()
        var product: Product
        var amount: Double
    }

    private var trimmedName: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var canSave: Bool { !trimmedName.isEmpty && !drafts.isEmpty }

    private var nutrition: Nutrition {
        drafts.map { $0.product.nutrition(forAmount: $0.amount) }.total
    }

    private var perServing: Nutrition {
        nutrition.scaled(by: 1 / Double(max(1, servings)))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Рецепт") {
                    TextField("Название", text: $name)
                    Stepper("Время: \(Fmt.minutes(minutes))", value: $minutes, in: 1...480, step: 5)
                    Stepper("Порций: \(servings)", value: $servings, in: 1...20)
                }

                Section("Фото") {
                    if let photoData, let image = UIImage(data: photoData) {
                        Image(uiImage: image)
                            .resizable()
                            .scaledToFill()
                            .frame(height: 180)
                            .frame(maxWidth: .infinity)
                            .clipShape(.rect(cornerRadius: 12))
                            .listRowInsets(EdgeInsets(top: 8, leading: 12, bottom: 8, trailing: 12))
                    }
                    PhotosPicker(selection: $photoItem, matching: .images) {
                        Label(photoData == nil ? "Выбрать фото" : "Заменить фото",
                              systemImage: "photo.on.rectangle")
                    }
                    if photoData != nil {
                        Button(role: .destructive) {
                            photoData = nil
                            photoItem = nil
                        } label: {
                            Label("Убрать фото", systemImage: "trash")
                        }
                    }
                }

                Section {
                    ForEach($drafts) { $draft in
                        HStack {
                            Image(systemName: CategoryStyle.symbol(for: draft.product.category))
                                .foregroundStyle(CategoryStyle.color(for: draft.product.category))
                                .frame(width: 24)
                            Text(draft.product.name)
                            Spacer()
                            TextField("0", value: $draft.amount,
                                      format: Fmt.amountStyle)
                                .keyboardType(.decimalPad)
                                .multilineTextAlignment(.trailing)
                                .monospacedDigit()
                                .frame(maxWidth: 80)
                            Text(draft.product.unit)
                                .foregroundStyle(.secondary)
                                .frame(width: 30, alignment: .leading)
                        }
                    }
                    .onDelete { drafts.remove(atOffsets: $0) }

                    Button {
                        isPickingProduct = true
                    } label: {
                        Label("Добавить ингредиент", systemImage: "plus.circle.fill")
                    }
                } header: {
                    Text("Ингредиенты на \(servings) порц.")
                } footer: {
                    if drafts.isEmpty {
                        Text("Рецепт без ингредиентов не сохранится — из него нечего списывать.")
                    }
                }

                Section("Шаги приготовления") {
                    TextField("Необязательно: как это готовить",
                              text: $steps, axis: .vertical)
                        .lineLimit(4...12)
                }

                Section("КБЖУ") {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("На порцию").font(.caption).foregroundStyle(.secondary)
                        NutritionRow(nutrition: perServing)
                        Text("Всё блюдо").font(.caption).foregroundStyle(.secondary)
                        NutritionRow(nutrition: nutrition)
                    }
                    .listRowInsets(EdgeInsets(top: 10, leading: 12, bottom: 10, trailing: 12))
                }
            }
            .navigationTitle(editing == nil ? "Новый рецепт" : "Правка рецепта")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Сохранить") { save() }.disabled(!canSave)
                }
            }
            .sheet(isPresented: $isPickingProduct) {
                ProductPicker(exclude: Set(drafts.map(\.product.name))) { product in
                    drafts.append(Draft(product: product, amount: defaultAmount(for: product)))
                }
            }
            .task(id: photoItem) {
                guard let photoItem else { return }
                photoData = try? await photoItem.loadTransferable(type: Data.self)
            }
            .onAppear(perform: loadForEditing)
        }
    }

    /// Разумная стартовая граммовка, чтобы не набирать её с нуля.
    private func defaultAmount(for product: Product) -> Double {
        switch product.unit {
        case "шт": 1
        case "мл": 100
        default: product.category == "Специи" || product.category == "Зелень" ? 5 : 100
        }
    }

    private func loadForEditing() {
        guard let editing, drafts.isEmpty, name.isEmpty else { return }
        name = editing.name
        minutes = editing.cookingTimeMinutes
        servings = editing.baseServings
        steps = editing.steps ?? ""
        photoData = editing.photoData
        drafts = editing.orderedIngredients.compactMap { ingredient in
            ingredient.product.map { Draft(product: $0, amount: ingredient.amountPerBaseServing) }
        }
    }

    private func save() {
        let recipe: Recipe
        if let editing {
            recipe = editing
            for ingredient in editing.ingredients { context.delete(ingredient) }
            recipe.ingredients.removeAll()
        } else {
            recipe = Recipe(name: trimmedName, cookingTimeMinutes: minutes,
                            baseServings: servings, isCustom: true)
            context.insert(recipe)
        }
        recipe.name = trimmedName
        recipe.cookingTimeMinutes = minutes
        recipe.baseServings = servings
        recipe.steps = steps.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? nil : steps
        recipe.photoData = photoData
        recipe.isCustom = true

        for (index, draft) in drafts.enumerated() {
            let ingredient = RecipeIngredient(product: draft.product,
                                              amountPerBaseServing: draft.amount,
                                              order: index)
            context.insert(ingredient)
            ingredient.recipe = recipe
            recipe.ingredients.append(ingredient)
        }

        try? context.save()
        Feedback.shared.play(.added)
        dismiss()
    }
}
