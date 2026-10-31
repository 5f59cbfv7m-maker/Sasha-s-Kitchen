#if !targetEnvironment(macCatalyst)
import SwiftUI
import VisionKit

/// Камера читает дату «годен до» с упаковки и подставляет её в форму.
struct DateScanSheet: View {
    @Environment(\.dismiss) private var dismiss
    let onDate: (Date) -> Void

    var body: some View {
        NavigationStack {
            VStack(spacing: 16) {
                DateScanner { date in
                    onDate(date)
                    Feedback.shared.play(.added)
                    dismiss()
                }
                .clipShape(.rect(cornerRadius: 18))
                Text("Наведите камеру на дату «годен до» на упаковке.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding()
            .navigationTitle("Срок годности")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Отмена") { dismiss() } }
            }
        }
    }

    static var isSupported: Bool { DataScannerViewController.isSupported }
}

private struct DateScanner: UIViewControllerRepresentable {
    let onDate: (Date) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(recognizedDataTypes: [.text()], qualityLevel: .accurate,
                                                isHighlightingEnabled: true)
        scanner.delegate = context.coordinator
        return scanner
    }

    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        if !scanner.isScanning { try? scanner.startScanning() }
    }

    func makeCoordinator() -> Coordinator { Coordinator(onDate: onDate) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onDate: (Date) -> Void
        private var done = false

        init(onDate: @escaping (Date) -> Void) { self.onDate = onDate }

        func dataScanner(_ scanner: DataScannerViewController, didAdd added: [RecognizedItem],
                         allItems: [RecognizedItem]) {
            guard !done else { return }
            for item in allItems {
                if case .text(let text) = item, let date = ExpiryDateParser.parse(text.transcript) {
                    done = true
                    scanner.stopScanning()
                    onDate(date)
                    return
                }
            }
        }
    }
}
#endif
