import PhotosUI
import SwiftUI
#if !targetEnvironment(macCatalyst)
import VisionKit
#endif

/// Импорт рецепта: ссылка на сайт или фото страницы книги, журнала,
/// открытки от бабушки. Дальше черновик открывается в редакторе.
struct RecipeImportView: View {
    @Environment(\.dismiss) private var dismiss
    let onImport: (ImportedRecipe) -> Void

    @State private var link = ""
    @State private var photoItem: PhotosPickerItem?
    @State private var isWorking = false
    @State private var isScanning = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("https://…", text: $link)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .onSubmit(importLink)
                    Button("Загрузить рецепт", action: importLink)
                        .disabled(URL(string: link.trimmingCharacters(in: .whitespaces))?.scheme == nil || isWorking)
                } header: {
                    Text("По ссылке")
                } footer: {
                    Text("Подойдёт страница рецепта на большинстве кулинарных сайтов: название, ингредиенты, шаги и фото подставятся сами.")
                }

                Section {
                    PhotosPicker(selection: $photoItem, matching: .images) {
                        Label { Text("Выбрать фото страницы") } icon: { Image(systemName: "photo.on.rectangle") }
                    }
                    #if !targetEnvironment(macCatalyst)
                    if VNDocumentCameraViewController.isSupported {
                        Button {
                            isScanning = true
                        } label: {
                            Label("Сфотографировать страницу", systemImage: "doc.viewfinder")
                        }
                    }
                    #endif
                } header: {
                    Text("С фото")
                } footer: {
                    Text("Текст распознаётся на устройстве и никуда не отправляется. Первая строка станет названием, строки с количеством — ингредиентами.")
                }

                if isWorking {
                    HStack { ProgressView(); Text("Читаем рецепт…").foregroundStyle(.secondary) }
                }
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.orange)
                }
            }
            .navigationTitle("Импорт рецепта")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Отмена") { dismiss() } }
            }
            .task(id: photoItem) {
                guard let photoItem else { return }
                guard let data = try? await photoItem.loadTransferable(type: Data.self),
                      let image = UIImage(data: data)?.cgImage else { return }
                await recognize([image], photo: data)
            }
            #if !targetEnvironment(macCatalyst)
            .fullScreenCover(isPresented: $isScanning) {
                DocumentScanner { images in
                    isScanning = false
                    Task { await recognize(images.compactMap(\.cgImage), photo: nil) }
                }
                .ignoresSafeArea()
            }
            #endif
        }
    }

    private func importLink() {
        guard let url = URL(string: link.trimmingCharacters(in: .whitespaces)), url.scheme != nil else { return }
        run { try await RecipeImport.fromWeb(url) }
    }

    private func recognize(_ images: [CGImage], photo: Data?) async {
        run {
            var lines: [String] = []
            for image in images { lines += try await RecipeImport.recognizeText(in: image) }
            guard var recipe = RecipeImport.fromText(lines) else { throw RecipeImport.ImportError.unreadable }
            recipe.photoData = photo
            return recipe
        }
    }

    private func run(_ work: @escaping () async throws -> ImportedRecipe) {
        isWorking = true
        error = nil
        Task {
            defer { isWorking = false }
            do {
                let recipe = try await work()
                Feedback.shared.play(.added)
                onImport(recipe)
                dismiss()
            } catch {
                Feedback.shared.play(.warning)
                self.error = error.localizedDescription
            }
        }
    }
}

#if !targetEnvironment(macCatalyst)
/// Системная съёмка документа: сама выравнивает страницу и убирает перспективу.
private struct DocumentScanner: UIViewControllerRepresentable {
    let onScan: ([UIImage]) -> Void

    func makeUIViewController(context: Context) -> VNDocumentCameraViewController {
        let controller = VNDocumentCameraViewController()
        controller.delegate = context.coordinator
        return controller
    }

    func updateUIViewController(_ controller: VNDocumentCameraViewController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(onScan: onScan) }

    final class Coordinator: NSObject, VNDocumentCameraViewControllerDelegate {
        let onScan: ([UIImage]) -> Void

        init(onScan: @escaping ([UIImage]) -> Void) { self.onScan = onScan }

        func documentCameraViewController(_ controller: VNDocumentCameraViewController,
                                          didFinishWith scan: VNDocumentCameraScan) {
            onScan((0..<scan.pageCount).map(scan.imageOfPage))
        }

        func documentCameraViewControllerDidCancel(_ controller: VNDocumentCameraViewController) {
            onScan([])
        }
    }
}
#endif
