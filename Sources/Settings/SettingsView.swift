import AVFoundation
import EventKit
import SwiftData
import SwiftUI

struct SettingsView: View {
    @Environment(\.modelContext) private var context
    @Query private var products: [Product]
    @Query private var recipes: [Recipe]
    @Query private var logs: [CookingLog]

    @AppStorage(Feedback.soundKey) private var soundEnabled = true
    @AppStorage(Feedback.hapticsKey) private var hapticsEnabled = true

    @State private var cameraStatus = CameraPermission.statusText
    @State private var remindersStatus = ""
    @State private var isResetting = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Toggle("Звуковые эффекты", isOn: $soundEnabled)
                    Toggle("Тактильный отклик", isOn: $hapticsEnabled)
                    Button {
                        Feedback.shared.play(.cooked)
                    } label: {
                        Label("Проверить звук", systemImage: "speaker.wave.2")
                    }
                } header: {
                    Text("Отклик")
                } footer: {
                    Text("На iPad нет Taptic Engine, поэтому вибро здесь — необязательный бонус. Основной отклик — звук и анимация.")
                }

                Section {
                    LabeledContent("Камера", value: cameraStatus)
                    Button {
                        Task {
                            await CameraPermission.request()
                            cameraStatus = CameraPermission.statusText
                        }
                    } label: {
                        Label("Запросить доступ к камере", systemImage: "camera")
                    }
                    .disabled(CameraPermission.status != .notDetermined)

                    Button {
                    } label: {
                        Label("Сканер штрихкода", systemImage: "barcode.viewfinder")
                    }
                    .disabled(true)
                } header: {
                    Text("Сканер штрихкода")
                } footer: {
                    Text("Сам сканер и запрос к Open Food Facts — вторая фаза. Разрешение на камеру можно выдать заранее, чтобы потом не отвлекаться.")
                }

                Section {
                    LabeledContent("Напоминания", value: remindersStatus)
                    Button {
                        Task {
                            await RemindersService.shared.requestAccess()
                            remindersStatus = Self.remindersText()
                        }
                    } label: {
                        Label("Запросить доступ к Напоминаниям", systemImage: "checklist")
                    }
                    .disabled(RemindersService.shared.authorizationStatus != .notDetermined)
                } header: {
                    Text("Список покупок")
                } footer: {
                    Text("Кнопка «В Напоминания» на вкладке «Для покупки» создаёт список «\(RemindersService.listName)» с настоящими чекбоксами.")
                }

                Section {
                    LabeledContent("Продуктов в каталоге", value: "\(products.count)")
                    LabeledContent("Рецептов", value: "\(recipes.count)")
                    LabeledContent("Записей в журнале", value: "\(logs.count)")
                    Button(role: .destructive) {
                        isResetting = true
                    } label: {
                        Label("Загрузить стартовые данные заново", systemImage: "arrow.clockwise")
                    }
                } header: {
                    Text("Данные")
                } footer: {
                    Text("Сброс удалит все остатки, свои рецепты и журнал, а затем разложит заново стартовые 49 продуктов и 40 рецептов.")
                }

                Section("О приложении") {
                    LabeledContent("Версия", value: Self.version)
                    LabeledContent("Хранение", value: "SwiftData, локально на устройстве")
                }
            }
            .navigationTitle("Настройки")
            .alert("Сбросить данные?", isPresented: $isResetting) {
                Button("Отмена", role: .cancel) {}
                Button("Сбросить", role: .destructive) {
                    SeedLoader.reset(context)
                    Feedback.shared.play(.cooked)
                }
            } message: {
                Text("Свои продукты, рецепты и журнал будут удалены без возможности вернуть.")
            }
            .onAppear {
                cameraStatus = CameraPermission.statusText
                remindersStatus = Self.remindersText()
            }
        }
    }

    private static func remindersText() -> String {
        switch EKEventStore.authorizationStatus(for: .reminder) {
        case .fullAccess: "Доступ разрешён"
        case .writeOnly: "Только запись"
        case .denied: "Доступ запрещён"
        case .restricted: "Доступ ограничен системой"
        case .notDetermined: "Разрешение ещё не запрашивалось"
        @unknown default: "Неизвестно"
        }
    }

    private static var version: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "1.0"
        let build = info?["CFBundleVersion"] as? String ?? "1"
        return "\(short) (\(build))"
    }
}
