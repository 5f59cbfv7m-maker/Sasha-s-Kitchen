import SwiftData
import SwiftUI
#if !targetEnvironment(macCatalyst)
import VisionKit
#endif

/// Сканер штрихкода: камера (на Mac и в симуляторе — ввод цифр руками) →
/// карточка продукта из своего каталога или из Open Food Facts → в список
/// покупок или в холодильник.
struct ScanSheet: View {
    /// Задан — сканер только подставляет найденное в форму нового продукта.
    var onPick: ((FoodFacts.Info) -> Void)?

    @Environment(\.dismiss) private var dismiss
    @Environment(\.modelContext) private var context
    @Query private var products: [Product]

    @State private var code = ""
    @State private var phase: Phase = .scanning
    @State private var adding: FoodFacts.Info?

    private enum Phase {
        case scanning, loading
        case found(FoodFacts.Info)
        case inCatalog(Product)
        case failed(barcode: String?, message: String)
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 20) {
                    switch phase {
                    case .scanning: scanner
                    case .loading: ProgressView("Ищем продукт…").padding(.top, 60)
                    case .found(let info): foundCard(info)
                    case .inCatalog(let product): catalogCard(product)
                    case .failed(let barcode, let message): failedCard(barcode: barcode, message: message)
                    }
                }
                .padding()
                .frame(maxWidth: 560)
                .frame(maxWidth: .infinity)
            }
            .navigationTitle("Штрихкод")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
            }
            // Новый продукт сохранён или отменён — сканер тоже закрываем.
            .sheet(item: $adding, onDismiss: { dismiss() }) { AddProductView(prefill: $0) }
        }
    }

    // MARK: Поиск

    @ViewBuilder
    private var scanner: some View {
        #if !targetEnvironment(macCatalyst)
        if DataScannerViewController.isSupported {
            CameraScanner(onCode: handle)
                .frame(height: 320)
                .clipShape(.rect(cornerRadius: 18))
            Text("Наведите камеру на штрихкод на упаковке.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        #endif
        HStack {
            TextField("Цифры под штрихкодом", text: $code)
                .keyboardType(.numberPad)
                .textFieldStyle(.roundedBorder)
                .onSubmit { handle(code) }
            Button("Найти") { handle(code) }
                .buttonStyle(.borderedProminent)
                .disabled(code.isEmpty)
        }
        Text("Сведения о продуктах — из открытой базы Open Food Facts. Отправляется только сам штрихкод.")
            .font(.footnote)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
    }

    private func handle(_ raw: String) {
        guard let barcode = FoodFacts.normalized(raw) else {
            phase = .failed(barcode: nil, message: FoodFacts.LookupError.badBarcode.localizedDescription)
            return
        }
        Feedback.shared.play(.added)
        // Свой каталог раньше интернета: продукт, однажды добавленный сканером, узнаётся сразу.
        if onPick == nil, let product = products.first(where: { $0.barcode == barcode }) {
            phase = .inCatalog(product)
            return
        }
        phase = .loading
        Task {
            do {
                phase = .found(try await FoodFacts.lookup(barcode))
            } catch {
                Feedback.shared.play(.warning)
                phase = .failed(barcode: barcode, message: error.localizedDescription)
            }
        }
    }

    // MARK: Карточки

    private func foundCard(_ info: FoodFacts.Info) -> some View {
        VStack(spacing: 16) {
            AsyncImage(url: info.imageURL) { image in
                image.resizable().scaledToFit()
            } placeholder: {
                Image(systemName: "barcode")
                    .font(.system(size: 44))
                    .foregroundStyle(.tertiary)
            }
            .frame(height: 140)

            VStack(spacing: 4) {
                Text(info.name).font(.title3.weight(.semibold)).multilineTextAlignment(.center)
                if let details = [info.brand, info.quantity].compactMap({ $0 }).joined(separator: " · ").nilIfEmpty {
                    Text(details).font(.subheadline).foregroundStyle(.secondary)
                }
                Text(info.barcode).font(.caption.monospaced()).foregroundStyle(.tertiary)
            }

            nutrition(info.per100)

            if let onPick {
                Button {
                    onPick(info)
                    dismiss()
                } label: {
                    Label("Подставить в форму", systemImage: "square.and.pencil").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
            } else {
                addToListButton(info.name)
                Button {
                    adding = info
                } label: {
                    Label("Положить в холодильник", systemImage: "refrigerator").frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
            }
            scanAgainButton
        }
    }

    private func catalogCard(_ product: Product) -> some View {
        let item = product.stockItems?.first
        return VStack(spacing: 16) {
            Image(systemName: CategoryStyle.symbol(for: product.category))
                .font(.system(size: 30, weight: .semibold))
                .foregroundStyle(.white)
                .frame(width: 72, height: 72)
                .background(CategoryStyle.color(for: product.category).gradient, in: .circle)
            VStack(spacing: 4) {
                Text(product.name).font(.title3.weight(.semibold))
                Text(item.map { "\(Fmt.category(product.category)) · " + String(localized: "в холодильнике \(product.amountLabel($0.currentAmount))") }
                     ?? Fmt.category(product.category))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            nutrition(product.nutritionPer100)
            addToListButton(product.name)
            if let item {
                Button {
                    Kitchen.replenish(item, by: Kitchen.suggestedPurchase(for: item))
                    try? context.save()
                    Feedback.shared.play(.added)
                    dismiss()
                } label: {
                    Label("Пополнить: \(product.amountLabel(Kitchen.suggestedPurchase(for: item)))",
                          systemImage: "plus.circle")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
            }
            scanAgainButton
        }
    }

    private func failedCard(barcode: String?, message: String) -> some View {
        VStack(spacing: 16) {
            Label(message, systemImage: "exclamationmark.magnifyingglass")
                .foregroundStyle(.orange)
                .multilineTextAlignment(.center)
            if let barcode, onPick == nil {
                // Продукта нет в базе — заводим сами, штрихкод запомнится.
                Button {
                    adding = FoodFacts.Info(barcode: barcode, name: "")
                } label: {
                    Label("Завести продукт с этим штрихкодом", systemImage: "plus").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
            }
            scanAgainButton
        }
        .padding(.top, 40)
    }

    @ViewBuilder
    private func nutrition(_ per100: Nutrition?) -> some View {
        if let per100 {
            VStack(alignment: .leading, spacing: 6) {
                Text("КБЖУ на 100 г").font(.caption).foregroundStyle(.secondary)
                NutritionRow(nutrition: per100)
            }
        } else {
            Text("КБЖУ в базе не указаны.").font(.footnote).foregroundStyle(.secondary)
        }
    }

    private func addToListButton(_ name: String) -> some View {
        Button {
            Kitchen.addToList(name, in: context)
            Feedback.shared.play(.added)
            dismiss()
        } label: {
            Label("В список покупок", systemImage: "cart.badge.plus").frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
    }

    private var scanAgainButton: some View {
        Button("Сканировать ещё") {
            code = ""
            phase = .scanning
        }
    }
}

extension FoodFacts.Info: Identifiable {
    var id: String { barcode }
}

#if !targetEnvironment(macCatalyst)
/// Системный сканер VisionKit. На Mac его нет — там остаётся ввод цифр.
private struct CameraScanner: UIViewControllerRepresentable {
    let onCode: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.ean13, .ean8, .upce])],
            isHighlightingEnabled: true)
        scanner.delegate = context.coordinator
        return scanner
    }

    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        if !scanner.isScanning { try? scanner.startScanning() }
    }

    func makeCoordinator() -> Coordinator { Coordinator(onCode: onCode) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onCode: (String) -> Void
        private var done = false

        init(onCode: @escaping (String) -> Void) { self.onCode = onCode }

        func dataScanner(_ scanner: DataScannerViewController, didAdd added: [RecognizedItem],
                         allItems: [RecognizedItem]) {
            // Один штрихкод на сеанс: камера видит его десятки раз в секунду.
            guard !done else { return }
            for item in added {
                if case .barcode(let barcode) = item, let payload = barcode.payloadStringValue {
                    done = true
                    scanner.stopScanning()
                    onCode(payload)
                    return
                }
            }
        }
    }
}
#endif
